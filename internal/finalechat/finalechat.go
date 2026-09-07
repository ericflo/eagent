// Package finalechat is a small client for Finalechat
// (https://www.finalechat.com/AGENTS.md), the app where agents message the
// user's phone: one thread per session, messages, questions with tappable
// options, and long-polled replies. Standard library only.
package finalechat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
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
		if v := os.Getenv("FINALECHAT_URL"); v != "" {
			c.BaseURL = v
		} else {
			c.BaseURL = DefaultBaseURL
		}
	}
	if c.Token == "" {
		return nil, false
	}
	return c, true
}

// ---- types --------------------------------------------------------------

// Message is one entry in a thread.
type Message struct {
	ID         string         `json:"id"`
	ThreadID   string         `json:"thread_id"`
	Sender     string         `json:"sender"` // agent | user | system
	Body       string         `json:"body"`
	Format     string         `json:"format"`
	Importance string         `json:"importance"`
	Meta       map[string]any `json:"meta"`
	CreatedAt  time.Time      `json:"created_at"`
}

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
}

// Me describes the account behind a token.
type Me struct {
	Auth        string `json:"auth"`
	BaseURL     string `json:"base_url"`
	PushEnabled bool   `json:"push_enabled"`
	Version     string `json:"version"`
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
}

func (e *Error) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("finalechat: HTTP %d %s", e.Status, e.Code)
	}
	return fmt.Sprintf("finalechat: %s (%s)", e.Message, e.Code)
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
func (c *Client) Post(ctx context.Context, ref string, req PostRequest) (Message, Thread, error) {
	var out struct {
		Message Message `json:"message"`
		Thread  Thread  `json:"thread"`
	}
	err := c.do(ctx, http.MethodPost, "/api/v1/threads/"+refPath(ref)+"/messages", nil, req, &out, 0)
	return out.Message, out.Thread, err
}

// Ask poses a question without waiting for the answer.
func (c *Client) Ask(ctx context.Context, ref string, req AskRequest) (Question, Thread, error) {
	var out struct {
		Question Question `json:"question"`
		Thread   Thread   `json:"thread"`
	}
	err := c.do(ctx, http.MethodPost, "/api/v1/threads/"+refPath(ref)+"/questions", nil, req, &out, 0)
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
	return out.Messages, out.TimedOut, err
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

// do performs one request. wait is the long-poll length the server was
// asked for, so the HTTP timeout can exceed it.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body any, out any, wait int) error {
	if c.Token == "" {
		return errors.New("finalechat: no token")
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	u := strings.TrimRight(c.BaseURL, "/") + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(raw)
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
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
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
		e := &Error{Status: resp.StatusCode}
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
			return fmt.Errorf("finalechat: bad response: %w", err)
		}
	}
	return nil
}
