// Package finalechat is a small client for Finalechat
// (https://www.finalechat.com/AGENTS.md), the app where agents message the
// user's phone: one thread per session, messages, questions with tappable
// options, and long-polled replies. Standard library only.
package finalechat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL is the public service.
const DefaultBaseURL = "https://www.finalechat.com"

// DefaultTokenEnv is where the token usually arrives.
const DefaultTokenEnv = "FINALECHAT_TOKEN"

// ErrDisabled reports the process-wide outbound kill switch. This is separate
// from the chat mirroring preference: artifacts and settings can be enabled
// independently, but none may override EAGENT_FINALECHAT=off.
var ErrDisabled = errors.New("FinaleChat is disabled by EAGENT_FINALECHAT")

func Disabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("EAGENT_FINALECHAT"))) {
	case "0", "off", "false", "no":
		return true
	default:
		return false
	}
}

// Client talks to one Finalechat account.
type Client struct {
	BaseURL string
	Token   string
	// Source says where the token came from, for diagnostics.
	Source string
	HTTP   *http.Client
	// UserAgent identifies the caller.
	UserAgent string
}

// Resolve finds a token the way the finalechat CLI does: the environment
// variable first, then ~/.config/finalechat/config.json. ok is false when
// there is no token anywhere.
func Resolve(tokenEnv, baseURL string) (*Client, bool) {
	if tokenEnv == "" {
		tokenEnv = DefaultTokenEnv
	}
	c := &Client{BaseURL: baseURL}
	if v := strings.TrimSpace(os.Getenv(tokenEnv)); v != "" {
		c.Token, c.Source = v, "$"+tokenEnv
	}
	if c.BaseURL == "" {
		// An explicit FINALECHAT_URL (a local or staging server) outranks
		// the address remembered by the CLI's config file.
		c.BaseURL = strings.TrimSpace(os.Getenv("FINALECHAT_URL"))
	}
	if c.Token == "" || c.BaseURL == "" {
		if home, err := os.UserHomeDir(); err == nil {
			path := filepath.Join(home, ".config", "finalechat", "config.json")
			if raw, err := os.ReadFile(path); err == nil {
				var f struct {
					Token   string `json:"token"`
					BaseURL string `json:"base_url"`
				}
				if json.Unmarshal(raw, &f) == nil {
					if c.Token == "" && strings.TrimSpace(f.Token) != "" {
						c.Token, c.Source = strings.TrimSpace(f.Token), "~/.config/finalechat/config.json"
					}
					if c.BaseURL == "" && f.BaseURL != "" {
						c.BaseURL = f.BaseURL
					}
				}
			}
		}
	}
	if c.BaseURL == "" {
		c.BaseURL = DefaultBaseURL
	}
	if c.Token == "" {
		return nil, false
	}
	return c, true
}

// ---- types --------------------------------------------------------------

// Message is one entry in a thread.
type Message struct {
	ID          string         `json:"id"`
	ThreadID    string         `json:"thread_id"`
	Sender      string         `json:"sender"` // agent | user | system
	Body        string         `json:"body"`
	Format      string         `json:"format"`
	Importance  string         `json:"importance"`
	Meta        map[string]any `json:"meta"`
	CreatedAt   time.Time      `json:"created_at"`
	Attachments []Attachment   `json:"attachments,omitempty"`
	// Origin says who really wrote it: "session" (the user, from the app) or
	// "token" (an agent, including this one mirroring terminal input). Rows
	// from before the field existed have "".
	Origin string `json:"origin,omitempty"`
	// Deleted marks a tombstone: a message removed since the last page. It
	// keeps paging anchors valid and carries no body.
	Deleted bool `json:"deleted,omitempty"`
}

// Attachment is a file on a message. URL and ThumbURL are relative to the
// base URL.
type Attachment struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"` // image | file
	ContentType string `json:"content_type"`
	Filename    string `json:"filename"`
	Size        int64  `json:"size"`
	Width       int    `json:"width,omitempty"`
	Height      int    `json:"height,omitempty"`
	URL         string `json:"url"`
	ThumbURL    string `json:"thumb_url,omitempty"`
}

// File is a local file to upload with a message.
type File struct {
	Name        string
	ContentType string
	Data        []byte
}

// Limits mirror the server's.
const (
	MaxAttachments    = 8
	MaxAttachmentSize = 10 << 20
)

// IsAnswer reports whether the message records the answer to a question,
// and returns that question's id.
func (m Message) IsAnswer() (string, bool) {
	if m.Meta == nil {
		return "", false
	}
	if kind, _ := m.Meta["kind"].(string); kind != "answer" {
		return "", false
	}
	id, _ := m.Meta["question_id"].(string)
	return id, id != ""
}

// Option is one tappable answer.
type Option struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// Answer is the user's response to a question.
type Answer struct {
	Selected []string `json:"selected"`
	Text     string   `json:"text,omitempty"`
}

// Question is a decision posed to the user.
type Question struct {
	ID            string         `json:"id"`
	ThreadID      string         `json:"thread_id"`
	Prompt        string         `json:"prompt"`
	Options       []Option       `json:"options"`
	AllowFreeform bool           `json:"allow_freeform"`
	MultiSelect   bool           `json:"multi_select"`
	Status        string         `json:"status"` // pending | answered | cancelled | expired
	Answer        *Answer        `json:"answer"`
	Meta          map[string]any `json:"meta"`
	CreatedAt     time.Time      `json:"created_at"`
	ExpiresAt     *time.Time     `json:"expires_at"`
}

// Thread is one conversation.
type Thread struct {
	ID               string     `json:"id"`
	ExternalID       string     `json:"external_id"`
	Title            string     `json:"title"`
	Agent            string     `json:"agent"`
	ArchivedAt       *time.Time `json:"archived_at"`
	Muted            bool       `json:"muted"`
	PendingQuestions int        `json:"pending_questions"`
	Activity         *Status    `json:"activity"` // the live status line, or nil
}

// Status is the live status line the app shows as a typing indicator.
type Status struct {
	Text      string    `json:"text"`
	Kind      string    `json:"kind"`
	At        time.Time `json:"at"`
	Since     time.Time `json:"since"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Activity sets the status line: one short line about what the agent is
// doing right now. It lapses after TTLSeconds unless set again, and a
// message or question from the agent clears it unless that post carries
// its own Activity.
type Activity struct {
	Text       string `json:"text"`
	Kind       string `json:"kind,omitempty"` // thinking | working | typing | waiting | tool
	TTLSeconds int    `json:"ttl_seconds,omitempty"`
	// Seq orders concurrent writers: a write whose seq is not above the live
	// status's is ignored. A nanosecond timestamp works.
	Seq int64 `json:"seq,omitempty"`
}

// Me describes the account behind a token.
type Me struct {
	Auth string `json:"auth"`
	// Features lists the deployment's optional capabilities: activity,
	// idempotency, dismiss, push, attachments. Older servers send none.
	Features    []string `json:"features"`
	BaseURL     string   `json:"base_url"`
	PushEnabled bool     `json:"push_enabled"`
	Version     string   `json:"version"`
	User        struct {
		DisplayName string `json:"display_name"`
		Email       string `json:"email"`
		Settings    struct {
			NotifyAllMessages bool `json:"notify_all_messages"`
			RemoteMode        bool `json:"remote_mode"`
		} `json:"settings"`
	} `json:"user"`
}

// PostRequest creates a message.
type PostRequest struct {
	Body       string         `json:"body"`
	Format     string         `json:"format,omitempty"`     // markdown (default) | text
	Importance string         `json:"importance,omitempty"` // normal (default) | important
	Sender     string         `json:"sender,omitempty"`     // agent (default) | user | system
	Notify     *bool          `json:"notify,omitempty"`
	Meta       map[string]any `json:"meta,omitempty"`
	Title      string         `json:"title,omitempty"` // only when this creates the ext: thread
	Agent      string         `json:"agent,omitempty"` // only when this creates the ext: thread
	// Activity is the status to show after this message, so the line does
	// not blink off while the agent keeps working.
	Activity *Activity `json:"activity,omitempty"`
	// ClientKey makes the post idempotent within the thread: a retry with the
	// same key returns the original message instead of a duplicate. Posts
	// carrying a key are retried on transport errors, 429, and 5xx.
	ClientKey string `json:"client_key,omitempty"`
	// Files are uploaded and attached in the same request (multipart).
	Files []File `json:"-"`
}

// AskRequest creates a question.
type AskRequest struct {
	Prompt         string         `json:"prompt"`
	Options        []Option       `json:"options,omitempty"`
	AllowFreeform  *bool          `json:"allow_freeform,omitempty"`
	MultiSelect    bool           `json:"multi_select,omitempty"`
	TimeoutSeconds int            `json:"timeout_seconds,omitempty"`
	Meta           map[string]any `json:"meta,omitempty"`
	Title          string         `json:"title,omitempty"`
	Agent          string         `json:"agent,omitempty"`
	Activity       *Activity      `json:"activity,omitempty"`
	ClientKey      string         `json:"client_key,omitempty"`
}

// PatchRequest updates a thread.
type PatchRequest struct {
	Title    string         `json:"title,omitempty"`
	Agent    string         `json:"agent,omitempty"`
	Archived *bool          `json:"archived,omitempty"`
	Muted    *bool          `json:"muted,omitempty"`
	Meta     map[string]any `json:"meta,omitempty"`
}

// Error is a Finalechat error envelope.
type Error struct {
	Status  int
	Code    string
	Message string
	// RetryAfter is the server's Retry-After on a 429, when it sent one.
	RetryAfter time.Duration
	// RequestID is the X-Request-Id to quote when reporting a problem.
	RequestID string
}

// Temporary reports whether a retry might succeed: 429 and 5xx.
func (e *Error) Temporary() bool { return e.Status == 429 || e.Status >= 500 }

func (e *Error) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("finalechat: HTTP %d %s", e.Status, e.Code)
	}
	return fmt.Sprintf("finalechat: %s (%s)", e.Message, e.Code)
}

// Has reports whether the deployment lists a feature.
func (m Me) Has(feature string) bool {
	for _, f := range m.Features {
		if f == feature {
			return true
		}
	}
	return false
}

// Ref names a thread by the external id you chose.
func Ref(externalID string) string { return "ext:" + externalID }

// ---- calls --------------------------------------------------------------

// Me returns the account behind the token; the cheapest way to prove it.
func (c *Client) Me(ctx context.Context) (Me, error) {
	var out Me
	err := c.do(ctx, http.MethodGet, "/api/v1/me", nil, nil, &out, 0)
	return out, err
}

// Post adds a message to the thread, creating an ext: thread on first use.
// With Files it becomes one multipart request that uploads and posts.
func (c *Client) Post(ctx context.Context, ref string, req PostRequest) (Message, Thread, error) {
	var out struct {
		Message Message `json:"message"`
		Thread  Thread  `json:"thread"`
	}
	path := "/api/v1/threads/" + refPath(ref) + "/messages"
	body, contentType, wait, err := postBody(req)
	if err != nil {
		return Message{}, Thread{}, err
	}
	err = c.send(ctx, http.MethodPost, path, contentType, body, &out, wait, req.ClientKey != "")
	return out.Message, out.Thread, err
}

// postBody encodes a message post: JSON, or multipart when it carries files.
func postBody(req PostRequest) (body []byte, contentType string, wait int, err error) {
	if len(req.Files) == 0 {
		raw, err := json.Marshal(req)
		return raw, "application/json", 0, err
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	field := func(k, v string) {
		if v != "" {
			_ = mw.WriteField(k, v)
		}
	}
	field("body", req.Body)
	field("format", req.Format)
	field("importance", req.Importance)
	field("sender", req.Sender)
	field("title", req.Title)
	field("agent", req.Agent)
	field("client_key", req.ClientKey)
	if req.Notify != nil {
		field("notify", strconv.FormatBool(*req.Notify))
	}
	if req.Meta != nil {
		raw, _ := json.Marshal(req.Meta)
		field("meta", string(raw))
	}
	if req.Activity != nil {
		raw, _ := json.Marshal(req.Activity)
		field("activity", string(raw))
	}
	for _, f := range req.Files {
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, strings.ReplaceAll(f.Name, `"`, "'")))
		if f.ContentType != "" {
			h.Set("Content-Type", f.ContentType)
		}
		pw, err := mw.CreatePart(h)
		if err != nil {
			return nil, "", 0, err
		}
		if _, err := pw.Write(f.Data); err != nil {
			return nil, "", 0, err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, "", 0, err
	}
	return buf.Bytes(), mw.FormDataContentType(), 60, nil
}

// retrySleep waits between attempts; tests shorten it.
var retrySleep = func(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// send performs one request, and when retry is set (the request is
// idempotent) tries up to three times on transport errors, 429 (honouring
// Retry-After) and 5xx, with a short backoff.
func (c *Client) send(ctx context.Context, method, path, contentType string, body []byte, out any, wait int, retry bool) error {
	attempts := 1
	if retry {
		attempts = 3
	}
	var err error
	for i := 0; i < attempts; i++ {
		if i > 0 {
			d := time.Duration(i) * time.Second
			var e *Error
			if errors.As(err, &e) && e.RetryAfter > 0 {
				d = e.RetryAfter
				if d > 10*time.Second {
					d = 10 * time.Second
				}
			}
			if !retrySleep(ctx, d) {
				return err
			}
		}
		var rdr io.Reader
		if body != nil {
			rdr = bytes.NewReader(body)
		}
		err = c.doRaw(ctx, method, path, nil, contentType, rdr, out, wait)
		if err == nil {
			return nil
		}
		var e *Error
		if errors.As(err, &e) && !e.Temporary() {
			return err // the request itself is wrong; trying again will not help
		}
		if ctx.Err() != nil {
			return err
		}
	}
	return err
}

// SetActivity sets or refreshes the status line. applied is false when the
// server kept a newer status (by seq) or the same text still had most of
// its life. A thread that does not exist yet is a 404: post a message first.
func (c *Client) SetActivity(ctx context.Context, ref string, a Activity) (bool, error) {
	var out struct {
		Applied bool `json:"applied"`
	}
	err := c.do(ctx, http.MethodPost, "/api/v1/threads/"+refPath(ref)+"/activity", nil, a, &out, 0)
	return out.Applied, err
}

// ClearActivity drops the status line. seq (nanoseconds, like Activity.Seq)
// records a watermark so a slower set with an older seq cannot bring the
// line back; 0 sends none. An unknown thread is not an error.
func (c *Client) ClearActivity(ctx context.Context, ref string, seq int64) error {
	q := url.Values{}
	if seq > 0 {
		q.Set("seq", strconv.FormatInt(seq, 10))
	}
	err := c.do(ctx, http.MethodDelete, "/api/v1/threads/"+refPath(ref)+"/activity", q, nil, nil, 0)
	var e *Error
	if errors.As(err, &e) && e.Status == 404 {
		return nil
	}
	return err
}

// Download fetches an attachment by its relative URL (or id) into w and
// returns the content type and filename the server sent.
func (c *Client) Download(ctx context.Context, urlOrID string, w io.Writer) (contentType, filename string, err error) {
	path := urlOrID
	if !strings.HasPrefix(path, "/") {
		path = "/api/v1/attachments/" + url.PathEscape(urlOrID)
	}
	if c.Token == "" {
		return "", "", errors.New("finalechat: no token")
	}
	hc := credentialHTTPClient(c.HTTP)
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.BaseURL, "/")+path, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := hc.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("finalechat: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", "", &Error{Status: resp.StatusCode, Message: strings.TrimSpace(string(raw))}
	}
	if _, err := io.Copy(w, io.LimitReader(resp.Body, MaxAttachmentSize+1)); err != nil {
		return "", "", fmt.Errorf("finalechat: %w", err)
	}
	filename = ""
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		if _, params, err := mime.ParseMediaType(cd); err == nil {
			filename = params["filename"]
		}
	}
	return resp.Header.Get("Content-Type"), filename, nil
}

// Ask poses a question without waiting for the answer.
func (c *Client) Ask(ctx context.Context, ref string, req AskRequest) (Question, Thread, error) {
	var out struct {
		Question Question `json:"question"`
		Thread   Thread   `json:"thread"`
	}
	raw, err := json.Marshal(req)
	if err != nil {
		return Question{}, Thread{}, err
	}
	err = c.send(ctx, http.MethodPost, "/api/v1/threads/"+refPath(ref)+"/questions", "application/json", raw, &out, 0, req.ClientKey != "")
	return out.Question, out.Thread, err
}

// Question fetches one question, holding the request open up to wait
// seconds while it is pending (0 returns at once).
func (c *Client) Question(ctx context.Context, id string, wait int) (Question, error) {
	var out struct {
		Question Question `json:"question"`
	}
	q := url.Values{}
	if wait > 0 {
		q.Set("wait", strconv.Itoa(wait))
	}
	err := c.do(ctx, http.MethodGet, "/api/v1/questions/"+url.PathEscape(id), q, nil, &out, wait)
	return out.Question, err
}

// Cancel withdraws a pending question. Already-resolved questions are not
// an error.
func (c *Client) Cancel(ctx context.Context, id string) error {
	err := c.do(ctx, http.MethodPost, "/api/v1/questions/"+url.PathEscape(id)+"/cancel", nil, map[string]any{}, nil, 0)
	var e *Error
	if errors.As(err, &e) && (e.Code == "already_resolved" || e.Status == 409) {
		return nil
	}
	return err
}

// Messages lists messages newer than after (ascending), holding the request
// open up to wait seconds for one to arrive. sender filters by author.
func (c *Client) Messages(ctx context.Context, ref, after, sender string, wait, limit int) ([]Message, bool, error) {
	var out struct {
		Messages []Message `json:"messages"`
		HasMore  bool      `json:"has_more"`
		TimedOut bool      `json:"timed_out"`
		// AnchorUnknown: the after= id is not in the thread (it was deleted
		// and recreated under the same external id); the page starts from
		// the thread's first message.
		AnchorUnknown bool `json:"anchor_unknown"`
	}
	q := url.Values{}
	if after != "" {
		q.Set("after", after)
	}
	if sender != "" {
		q.Set("sender", sender)
	}
	if wait > 0 && after != "" {
		q.Set("wait", strconv.Itoa(wait))
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	err := c.do(ctx, http.MethodGet, "/api/v1/threads/"+refPath(ref)+"/messages", q, nil, &out, wait)
	return out.Messages, out.TimedOut || out.AnchorUnknown, err
}

// MessagesPage is Messages with the page's own flags: timedOut when the
// long poll ended without news, anchorUnknown when the after= id is gone.
func (c *Client) MessagesPage(ctx context.Context, ref, after, sender string, wait, limit int) (msgs []Message, timedOut, anchorUnknown bool, err error) {
	var out struct {
		Messages      []Message `json:"messages"`
		HasMore       bool      `json:"has_more"`
		TimedOut      bool      `json:"timed_out"`
		AnchorUnknown bool      `json:"anchor_unknown"`
	}
	q := url.Values{}
	if after != "" {
		q.Set("after", after)
	}
	if sender != "" {
		q.Set("sender", sender)
	}
	if wait > 0 && after != "" {
		q.Set("wait", strconv.Itoa(wait))
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	err = c.do(ctx, http.MethodGet, "/api/v1/threads/"+refPath(ref)+"/messages", q, nil, &out, wait)
	return out.Messages, out.TimedOut, out.AnchorUnknown, err
}

// Patch updates a thread's title, agent, archived, or muted state.
func (c *Client) Patch(ctx context.Context, ref string, req PatchRequest) (Thread, error) {
	var out struct {
		Thread Thread `json:"thread"`
	}
	err := c.do(ctx, http.MethodPatch, "/api/v1/threads/"+refPath(ref), nil, req, &out, 0)
	if err != nil {
		return Thread{}, err
	}
	return out.Thread, nil
}

// Thread fetches one thread.
func (c *Client) Thread(ctx context.Context, ref string) (Thread, error) {
	var out struct {
		Thread Thread `json:"thread"`
	}
	err := c.do(ctx, http.MethodGet, "/api/v1/threads/"+refPath(ref), nil, nil, &out, 0)
	return out.Thread, err
}

func refPath(ref string) string {
	if strings.HasPrefix(ref, "ext:") {
		return "ext:" + url.PathEscape(strings.TrimPrefix(ref, "ext:"))
	}
	return url.PathEscape(ref)
}

// do performs one JSON request. wait is the long-poll length the server was
// asked for, so the HTTP timeout can exceed it.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body any, out any, wait int) error {
	var rdr io.Reader
	contentType := ""
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(raw)
		contentType = "application/json"
	}
	return c.doRaw(ctx, method, path, query, contentType, rdr, out, wait)
}

// Events streams the account's server-sent events until ctx ends, the server
// asks for a reconnect (returns nil), or the connection drops (returns an
// error). fn receives each event's name and data; returning an error stops
// the stream. A stream that goes 45 seconds without any event is dropped,
// since the server pings every 20.
func (c *Client) Events(ctx context.Context, fn func(name string, data []byte) error) error {
	if c.Token == "" {
		return errors.New("finalechat: no token")
	}
	hc := credentialHTTPClient(c.HTTP)
	hc.Timeout = 0 // a stream has no overall deadline; the watchdog below has
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.BaseURL, "/")+"/api/v1/events", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "text/event-stream")
	ua := c.UserAgent
	if ua == "" {
		ua = "eagent"
	}
	req.Header.Set("User-Agent", ua)
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("finalechat: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		e := &Error{Status: resp.StatusCode, RequestID: resp.Header.Get("X-Request-Id")}
		var env struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(raw, &env) == nil {
			e.Code, e.Message = env.Error.Code, env.Error.Message
		}
		return e
	}
	activity := make(chan struct{}, 1)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-activity:
			case <-time.After(45 * time.Second):
				cancel()
				return
			}
		}
	}()
	reader := bufio.NewReaderSize(resp.Body, 64<<10)
	name := ""
	var data bytes.Buffer
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("finalechat: event stream ended: %w", err)
		}
		select {
		case activity <- struct{}{}:
		default:
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case line == "":
			if name == "" && data.Len() == 0 {
				continue
			}
			if name == "reconnect" {
				return nil
			}
			if err := fn(name, data.Bytes()); err != nil {
				return err
			}
			name = ""
			data.Reset()
		case strings.HasPrefix(line, ":"):
		case strings.HasPrefix(line, "event:"):
			name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			if data.Len() > 4<<20 {
				return errors.New("finalechat: event too large")
			}
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
}

// doRaw performs one request with a prepared body.
// API credentials belong to the configured service, including its port. Never
// forward them through a redirect (Go otherwise forwards to another local port).
func credentialHTTPClient(client *http.Client) *http.Client {
	if client == nil {
		client = http.DefaultClient
	}
	copy := *client
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &copy
}

func (c *Client) doRaw(ctx context.Context, method, path string, query url.Values, contentType string, rdr io.Reader, out any, wait int) error {
	if c.Token == "" {
		return errors.New("finalechat: no token")
	}
	hc := credentialHTTPClient(c.HTTP)
	u := strings.TrimRight(c.BaseURL, "/") + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	timeout := 30 * time.Second
	if wait > 0 {
		timeout = time.Duration(wait+30) * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	ua := c.UserAgent
	if ua == "" {
		ua = "eagent"
	}
	req.Header.Set("User-Agent", ua)
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("finalechat: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("finalechat: %w", err)
	}
	if resp.StatusCode >= 300 {
		e := &Error{Status: resp.StatusCode, RequestID: resp.Header.Get("X-Request-Id")}
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			if secs, err := strconv.Atoi(strings.TrimSpace(ra)); err == nil && secs > 0 {
				e.RetryAfter = time.Duration(secs) * time.Second
			}
		}
		var env struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(raw, &env) == nil {
			e.Code, e.Message = env.Error.Code, env.Error.Message
		}
		if e.Message == "" {
			e.Message = strings.TrimSpace(string(raw))
			if len(e.Message) > 200 {
				e.Message = e.Message[:200]
			}
		}
		return e
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			// The server accepted the request; only the reply is unreadable.
			// An Error with a 2xx status is never retried.
			return &Error{Status: resp.StatusCode, Code: "bad_response", Message: "bad response: " + err.Error(), RequestID: resp.Header.Get("X-Request-Id")}
		}
	}
	return nil
}
