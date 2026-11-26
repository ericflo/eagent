// Package llm talks to model providers through one provider-neutral request
// shape. Three wire protocols are implemented: the OpenAI Responses API
// (OpenAI, OpenRouter), OpenAI-style Chat Completions (Together AI, OpenRouter,
// anything compatible), and the Anthropic Messages API.
//
// All requests stream. Streaming is what lets the harness tell a slow model
// from a dead connection: a call that produces no bytes for IdleTimeout is
// aborted and retried instead of hanging for minutes.
package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif" // image.DecodeConfig reads the size of these
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ericflo/eagent/internal/netx"

	"github.com/ericflo/eagent/internal/event"
)

// Protocol names.
const (
	ProtocolResponses = "openai-responses"
	ProtocolChat      = "openai-chat"
	ProtocolAnthropic = "anthropic"
)

// Endpoint identifies a model behind a protocol.
type Endpoint struct {
	Protocol string `json:"protocol"`
	BaseURL  string `json:"base_url"`
	Model    string `json:"model"`
	APIKey   string `json:"-"`
	// ReasoningEffort is passed through where the provider supports it
	// ("none", "low", "medium", "high"). Empty means provider default.
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	// ReplayReasoning sends a model its own reasoning back on later turns
	// (encrypted reasoning items on OpenAI Responses; reasoning_content on
	// OpenAI Chat Completions) so multi-turn prefix caches stay intact.
	// On by default.
	ReplayReasoning bool `json:"replay_reasoning,omitempty"`
	MaxTokens       int  `json:"max_tokens,omitempty"`
	// Headers are extra request headers (e.g. OpenRouter attribution).
	Headers map[string]string `json:"-"`
}

// String renders "model @ host".
func (e Endpoint) String() string {
	host := e.BaseURL
	host = strings.TrimPrefix(host, "https://")
	host = strings.TrimPrefix(host, "http://")
	if i := strings.IndexByte(host, '/'); i > 0 {
		host = host[:i]
	}
	return e.Model + " @ " + host
}

// Tool is a function the model may call.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// Message is one turn of provider-neutral history.
type Message struct {
	Role string // "user" | "assistant" | "tool"

	// User / assistant text.
	Text string

	// Assistant tool calls.
	ToolCalls []event.ToolCall

	// Tool results (Role == "tool"). One Message may carry several results;
	// adapters split or merge them as the wire format requires.
	Results []ToolResult

	// Native is the provider-specific representation of an assistant turn
	// (Responses output items, Anthropic content blocks). When present and
	// produced by the same protocol, it is replayed verbatim so reasoning
	// state survives; otherwise the adapter rebuilds the turn from Text and
	// ToolCalls.
	Native         json.RawMessage
	NativeProtocol string
	// NativeHost is the base URL that produced Native. Encrypted reasoning
	// and item ids are bound to the account that minted them, so a fallback
	// route at another host must rebuild the turn instead of replaying.
	NativeHost string
	// NativeModel is the model that produced Native: the one the request
	// asked for, or, for a record that only kept the name the response
	// gave, that name (which can be a dated variant of the one asked for).
	// Empty when the record does not say.
	NativeModel string

	// Images are pictures attached to a user message (screenshots the user
	// sent). Adapters inline them as base64 for models that accept images;
	// a model that rejects them gets the text alone.
	Images []Image
}

// Image is a picture on disk to show the model.
type Image struct {
	Path      string
	MediaType string // image/png, image/jpeg, image/gif, image/webp
	// Refused says the record holds a provider's refusal of this picture
	// (Response.RefusedImages, written down by the caller): it is not
	// uploaded again, and a note stands in its place, the same note on
	// every call and after a restart.
	Refused bool
}

// maxInlineImage caps what is inlined into a request.
const maxInlineImage = 10 << 20

// Anthropic refuses a picture over 10 MB once base64-encoded, or over 8000
// pixels on a side (the Messages API's vision limits). Bedrock and Google's
// own Vertex take 5 MB; a refusal there is handled when it comes
// (Client.refusedImages).
const (
	anthropicMaxImageBase64 = 10 << 20
	anthropicMaxImageSide   = 8000
)

// tooBigFor reports why the provider behind protocol will certainly refuse
// the picture, or "" when it may take it. It looks at the file alone, so
// the same record always renders the same request.
func (im Image) tooBigFor(protocol string) string {
	st, err := os.Stat(im.Path)
	if err != nil {
		return "" // unreadable: the adapter leaves it out, as before
	}
	if st.Size() > maxInlineImage {
		// Never sent: the adapter leaves it out without a word, as it
		// always has, so a turn that held one renders as it did.
		return ""
	}
	if protocol != ProtocolAnthropic {
		return ""
	}
	if enc := (st.Size() + 2) / 3 * 4; enc > anthropicMaxImageBase64 {
		return fmt.Sprintf("it is %.1f MB encoded, over the %d MB this provider takes", float64(enc)/(1<<20), anthropicMaxImageBase64>>20)
	}
	if f, err := os.Open(im.Path); err == nil {
		cfg, _, err := image.DecodeConfig(f)
		f.Close()
		if err == nil && (cfg.Width > anthropicMaxImageSide || cfg.Height > anthropicMaxImageSide) {
			return fmt.Sprintf("it is %dx%d pixels, over the %d a side this provider takes", cfg.Width, cfg.Height, anthropicMaxImageSide)
		}
	}
	return ""
}

// fitImages replaces each picture the provider will certainly refuse for
// its size with a note saying so, instead of uploading it to be refused.
func fitImages(req Request, protocol string) Request {
	return replaceImages(req, func(im Image) string {
		if why := im.tooBigFor(protocol); why != "" {
			return "[" + filepath.Base(im.Path) + " could not be shown to this model: " + why + ". The file is on disk.]"
		}
		return ""
	})
}

// replaceImages returns the request with each picture for which note says
// something taken out and the note added to its message's text. The
// request is returned as it is when nothing is replaced.
func replaceImages(req Request, note func(Image) string) Request {
	var out []Message
	for i, m := range req.Messages {
		var kept []Image
		var notes []string
		for _, im := range m.Images {
			if n := note(im); n != "" {
				notes = append(notes, n)
			} else {
				kept = append(kept, im)
			}
		}
		if len(notes) == 0 {
			continue
		}
		if out == nil {
			out = append([]Message(nil), req.Messages...)
		}
		m.Images = kept
		m.Text = strings.TrimSpace(m.Text + "\n" + strings.Join(notes, "\n"))
		out[i] = m
	}
	if out != nil {
		req.Messages = out
	}
	return req
}

// WithRefused returns the request with the pictures at paths marked as
// refused (Image.Refused), as the record will mark them on every later
// request. A caller that has just written a refusal down and goes on with
// the same request through another client (a fallback route) uses it, so
// that client is not sent a picture the record says was refused, and what
// it is sent is what the conversation renders from then on.
func (req Request) WithRefused(paths []string) Request {
	if len(paths) == 0 {
		return req
	}
	refused := make(map[string]bool, len(paths))
	for _, p := range paths {
		refused[p] = true
	}
	var out []Message
	for i, m := range req.Messages {
		var images []Image
		for j, im := range m.Images {
			if !refused[im.Path] || im.Refused {
				continue
			}
			if images == nil {
				images = append([]Image(nil), m.Images...)
			}
			images[j].Refused = true
		}
		if images == nil {
			continue
		}
		if out == nil {
			out = append([]Message(nil), req.Messages...)
		}
		m.Images = images
		out[i] = m
	}
	if out != nil {
		req.Messages = out
	}
	return req
}

// imagePaths lists the pictures a request carries.
func imagePaths(req Request) []string {
	var out []string
	for _, m := range req.Messages {
		for _, im := range m.Images {
			out = append(out, im.Path)
		}
	}
	return out
}

// imageMemory remembers, for one client, the pictures its provider took
// and the ones it refused for a reason that could not be told in advance
// (undecodable, a limit of a gateway's own). A refused picture is left out
// of every later request with a note, instead of being uploaded to be
// refused again; the session's pictures are copies that never change, so
// the path names the bytes.
type imageMemory struct {
	mu       sync.Mutex
	accepted map[string]bool
	refused  map[string]bool
	// unreported are refusals no caller has been told of yet: a call
	// that left a picture out and then failed for another reason reports
	// nothing, and the refusal must still reach the record.
	unreported []string
}

// report hands over the refusals not yet told to a caller.
func (m *imageMemory) report() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := m.unreported
	m.unreported = nil
	return out
}

func (m *imageMemory) accept(paths []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.accepted == nil {
		m.accepted = map[string]bool{}
	}
	for _, p := range paths {
		m.accepted[p] = true
	}
}

// refuse remembers which pictures of a refused request the refusal is
// about, and keeps the new ones to be reported. It is about a picture the
// provider has not taken: one this client never saw go through, and that
// came after the model's last reply (what stands before a reply went
// through in the call that produced it, whichever client made it). When
// there is no such picture, every picture this client never saw go
// through; when there is none of those either, or all is set, all of them.
func (m *imageMemory) refuse(req Request, all bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.refused == nil {
		m.refused = map[string]bool{}
	}
	last := -1
	for i, msg := range req.Messages {
		if msg.Role == "assistant" {
			last = i
		}
	}
	var paths, unseen, fresh []string
	for i, msg := range req.Messages {
		for _, im := range msg.Images {
			paths = append(paths, im.Path)
			if m.accepted[im.Path] {
				continue
			}
			unseen = append(unseen, im.Path)
			if i > last {
				fresh = append(fresh, im.Path)
			}
		}
	}
	switch {
	case all:
		fresh = paths
	case len(fresh) == 0 && len(unseen) > 0:
		fresh = unseen
	case len(fresh) == 0:
		fresh = paths
	}
	for _, p := range fresh {
		if !m.refused[p] {
			m.refused[p] = true
			m.unreported = append(m.unreported, p)
		}
	}
}

// leaveOut replaces each refused picture with a note: one this client's
// provider refused, or one the record says was refused.
func (m *imageMemory) leaveOut(req Request) Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	return replaceImages(req, func(im Image) string {
		if im.Refused || m.refused[im.Path] {
			return "[" + filepath.Base(im.Path) + " could not be shown to this model: the provider refused it. The file is on disk.]"
		}
		return ""
	})
}

// dataURL reads the image and returns a data: URL, or "" when unreadable.
func (im Image) dataURL() string {
	raw, mt := im.bytes()
	if raw == nil {
		return ""
	}
	return "data:" + mt + ";base64," + base64.StdEncoding.EncodeToString(raw)
}

// bytes reads the image, sniffing the media type when it was not given.
func (im Image) bytes() ([]byte, string) {
	raw, err := os.ReadFile(im.Path)
	if err != nil || len(raw) == 0 || len(raw) > maxInlineImage {
		return nil, ""
	}
	mt := im.MediaType
	if mt == "" {
		mt = http.DetectContentType(raw)
	}
	return raw, mt
}

// hasImages reports whether any message carries an image.
func hasImages(req Request) bool {
	for _, m := range req.Messages {
		if len(m.Images) > 0 {
			return true
		}
	}
	return false
}

// withoutImages returns the request with images replaced by a note, for
// models that only read text.
func withoutImages(req Request) Request {
	out := req
	out.Messages = make([]Message, len(req.Messages))
	for i, m := range req.Messages {
		if len(m.Images) > 0 {
			m.Text = strings.TrimSpace(m.Text + "\n[The attached images could not be shown to this model; the files are on disk at the paths above.]")
			m.Images = nil
		}
		out.Messages[i] = m
	}
	return out
}

// RejectsImages reports a 4xx that means the model does not take pictures.
func (e *APIError) RejectsImages() bool {
	// 400/422 from the model, or a gateway's 404 "no endpoints support
	// image input" (OpenRouter phrases it that way).
	if e.Status != 400 && e.Status != 422 && e.Status != 404 {
		return false
	}
	b := strings.ToLower(e.Body)
	for _, k := range []string{"image", "vision", "multimodal", "input_image", "content must be a string", "invalid content", "unsupported content"} {
		if strings.Contains(b, k) {
			return true
		}
	}
	return false
}

// badImage reports a 4xx about this particular picture (too big, wrong
// dimensions, undecodable) rather than a model that cannot see at all.
// Such a refusal must not be remembered: the next picture may be fine.
func (e *APIError) badImage() bool {
	b := strings.ToLower(e.Body)
	// A complaint about the content type itself ("input tag 'image_url' ...
	// does not match any of the expected tags: 'text'") is a model that
	// cannot see at all, not a picture it disliked: let it be remembered.
	if strings.Contains(b, "expected tags") || strings.Contains(b, "union_tag_invalid") {
		return false
	}
	for _, k := range []string{"exceeds", "too large", "too big", "dimension", "could not process image", "unable to process image", "invalid base64", "image does not match", "media type does not match",
		"unsupported image", "invalid_image_format", "image format", "media_type", "could not decode", "unable to decode", "not a valid image"} {
		if strings.Contains(b, k) {
			return true
		}
	}
	return false
}

// ToolResult is the outcome of one tool call.
type ToolResult struct {
	CallID  string
	Name    string
	Output  string
	IsError bool
}

// Request is one model call.
type Request struct {
	System    string
	Messages  []Message
	Tools     []Tool
	MaxTokens int
	// CacheKey routes to the same cache shard (OpenAI prompt_cache_key).
	CacheKey string
}

// Response is the model's reply.
type Response struct {
	Text      string
	Reasoning string // human-readable summary when the provider exposes one
	ToolCalls []event.ToolCall
	Native    json.RawMessage
	Protocol  string
	BaseURL   string // the endpoint that produced this response
	Model     string // as the response named it; the endpoint's model when it named none
	Asked     string // the model the request asked for
	// RefusedImages are the pictures the provider refused that have not
	// been reported yet: during this call, which went through without
	// them, or during an earlier one through this client that failed.
	// The caller writes them down and marks them (Image.Refused) from
	// then on, so the request stays the same whichever client renders it.
	RefusedImages []string
	Usage         event.Usage
	Stop          string // "stop" | "tool_calls" | "length" | other provider value
	Elapsed       time.Duration
}

// By is the model to hold this response's native items to when they are
// replayed: the one that was asked for. A provider may name the model that
// ran in its own way, and that name need not look like the one asked for.
func (r *Response) By() string {
	if r.Asked != "" {
		return r.Asked
	}
	return r.Model
}

// Truncated reports whether the output hit the token limit.
func (r *Response) Truncated() bool { return r.Stop == "length" }

// Observer receives streaming deltas for display. Methods may be nil.
type Observer struct {
	Text      func(delta string)
	Reasoning func(delta string)
	ToolCall  func(name string)
	// Reset is called before a retry so a display does not keep showing
	// progress from the failed attempt.
	Reset func()
}

func (o *Observer) reset() {
	if o != nil && o.Reset != nil {
		o.Reset()
	}
}

func (o *Observer) text(s string) {
	if o != nil && o.Text != nil && s != "" {
		o.Text(s)
	}
}
func (o *Observer) reasoning(s string) {
	if o != nil && o.Reasoning != nil && s != "" {
		o.Reasoning(s)
	}
}
func (o *Observer) toolCall(n string) {
	if o != nil && o.ToolCall != nil {
		o.ToolCall(n)
	}
}

// Client sends requests to one endpoint with retries.
type Client struct {
	Endpoint Endpoint
	HTTP     *http.Client
	// IdleTimeout aborts a stream that produces no bytes for this long.
	IdleTimeout time.Duration
	// CallTimeout bounds one attempt end to end (zero = none).
	CallTimeout time.Duration
	// MaxAttempts bounds retries of transient failures.
	MaxAttempts int
	// OnRetry is called before each retry with the failure.
	OnRetry func(attempt int, err error, wait time.Duration)
	// UserAgent is sent with every request. Some CDNs reject empty or
	// library-default agents.
	UserAgent string

	// anthropicBudgeted remembers whether this model wants the older
	// budget-style thinking parameter instead of adaptive thinking.
	anthropicBudgeted atomic.Bool
	// anthropicCached remembers where the last requests Anthropic took
	// from this client left cache entries (anthropicBridge).
	anthropicCached anthropicEntries
	// rejectsImages remembers that this model refused pictures, so later
	// calls send the text alone instead of re-uploading and re-failing.
	rejectsImages atomic.Bool
	// images remembers the pictures this model took and the ones it
	// refused, so that a refused one is not uploaded on every later call.
	images imageMemory
	// chatNoCacheKey remembers that this Chat Completions endpoint rejected
	// prompt_cache_key, so later calls omit it.
	chatNoCacheKey atomic.Bool
	// chatNoReplayReasoning remembers that this Chat Completions endpoint
	// rejected assistant reasoning_content/reasoning on input.
	chatNoReplayReasoning atomic.Bool
}

// NewClient returns a client with sensible defaults.
func NewClient(ep Endpoint) *Client {
	return &Client{
		Endpoint:    ep,
		HTTP:        &http.Client{Transport: netx.Transport(), CheckRedirect: netx.SameOriginRedirects}, // per-request deadlines are set via context
		IdleTimeout: 120 * time.Second,
		CallTimeout: 15 * time.Minute,
		MaxAttempts: 6,
		UserAgent:   "eagent/1.0 (+https://github.com/ericflo/eagent)",
	}
}

// APIError is a non-2xx response.
type APIError struct {
	Status     int
	Body       string
	Code       string // provider error code when parseable
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	body := e.Body
	if len(body) > 600 {
		body = body[:600] + "..."
	}
	return fmt.Sprintf("api error %d: %s", e.Status, strings.TrimSpace(body))
}

// Retryable reports whether the failure is plausibly transient.
func (e *APIError) Retryable() bool {
	if e.QuotaExhausted() {
		return false // no credits: retrying cannot help, a fallback route can
	}
	switch e.Status {
	case 408, 409, 425, 500, 502, 503, 504, 520, 521, 522, 523, 524, 529:
		return true
	case 429:
		return true
	}
	return false
}

// QuotaExhausted reports a billing / credits failure, which should trigger a
// fallback route rather than a retry.
func (e *APIError) QuotaExhausted() bool {
	b := strings.ToLower(e.Body)
	// 402 is Payment Required: Anthropic's billing_error, OpenRouter's
	// "requires more credits".
	return e.Status == 402 || e.Code == "insufficient_quota" || e.Code == "credit_balance_exhausted" || e.Code == "billing_error" ||
		strings.Contains(b, "insufficient_quota") || strings.Contains(b, "no credits") ||
		strings.Contains(b, "credit balance") || strings.Contains(b, "insufficient credits") ||
		strings.Contains(b, "requires more credits")
}

// Unroutable reports that this endpoint cannot serve the model at all
// (bad key, no access, unknown model, no credits). The caller should try a
// fallback endpoint.
func (e *APIError) Unroutable() bool {
	if e.QuotaExhausted() {
		return true
	}
	switch e.Status {
	case 401, 403:
		return true
	case 404:
		return true
	case 400:
		b := strings.ToLower(e.Body)
		return strings.Contains(b, "model_not_found") || strings.Contains(b, "does not exist") ||
			strings.Contains(b, "invalid model") || strings.Contains(b, "not a valid model")
	}
	return false
}

// ContextOverflow reports that the prompt was too long for the model.
func (e *APIError) ContextOverflow() bool {
	if e.Status != 400 && e.Status != 413 && e.Status != 422 {
		return false
	}
	b := strings.ToLower(e.Body)
	if e.Status == 413 {
		// Anthropic's request_too_large is a body over its size limit. A
		// request with pictures has those dropped first (Complete); one
		// without is a conversation too long to send, which a dossier
		// fixes. A gateway's bare 413 says nothing about which it is.
		return strings.Contains(b, "request_too_large")
	}
	return strings.Contains(b, "context_length") || strings.Contains(b, "context length") ||
		strings.Contains(b, "maximum context") || strings.Contains(b, "too many tokens") ||
		strings.Contains(b, "prompt is too long") || strings.Contains(b, "exceeds the limit") ||
		strings.Contains(b, "input tokens exceed") ||
		// Anthropic: "input length and `max_tokens` exceed context limit".
		strings.Contains(b, "exceed context limit")
}

// streamError builds the error for a failure reported inside a stream,
// which arrives after a 200 and so carries no HTTP status of its own. The
// provider's code or type gives it one, so that it is judged as the same
// failure in a plain response would be (an overflow, missing credits, a
// model this key cannot use). Only a failure that names nothing is taken
// for a transient one, as before.
func streamError(code any, kind, message string, raw []byte) *APIError {
	e := &APIError{Status: 502, Body: message}
	if len(raw) > 0 {
		// The whole error object, as for a plain response, so that the
		// code and type are matched as well as the message.
		e.Body = string(raw)
	}
	if e.Body == "" {
		e.Body = "stream error"
	}
	if n, ok := code.(int); ok {
		code = float64(n)
	}
	switch v := code.(type) {
	case float64:
		if n := int(v); n >= 400 && n <= 599 {
			e.Status = n
			return e
		}
	case string:
		e.Code = v
	}
	if e.Code == "" {
		e.Code = kind
	}
	for _, k := range []string{e.Code, kind} {
		if s, ok := streamStatus[k]; ok {
			e.Status = s
			return e
		}
		if strings.Contains(k, "image") {
			e.Status = 400 // OpenAI's invalid_image, image_too_large, ...
			return e
		}
	}
	return e
}

// streamStatus maps the error codes and types providers put in a stream to
// the status the same error has in a plain response.
var streamStatus = map[string]int{
	// Anthropic error types.
	"invalid_request_error": 400,
	"authentication_error":  401,
	"billing_error":         402,
	"permission_error":      403,
	"not_found_error":       404,
	"request_too_large":     413,
	"rate_limit_error":      429,
	"api_error":             500,
	"timeout_error":         504,
	"overloaded_error":      529,
	// OpenAI error codes.
	"context_length_exceeded": 400,
	"invalid_prompt":          400,
	"model_not_found":         404,
	"insufficient_quota":      429,
	"rate_limit_exceeded":     429,
	"server_error":            500,
}

// ErrIdle is returned when a stream stalls.
var ErrIdle = errors.New("stream idle timeout")

// ErrTruncatedStream is returned when a stream closes before the provider
// signalled completion.
var ErrTruncatedStream = errors.New("stream ended before completion")

// TakeRefusedImages returns the pictures a provider refused that no
// response has reported: those of a call that left a picture out and then
// failed for another reason. A caller that keeps a record asks after a
// failed call, so the refusal is written down all the same.
func (c *Client) TakeRefusedImages() []string { return c.images.report() }

// Complete performs one model call with retries on transient failures.
func (c *Client) Complete(ctx context.Context, req Request, obs *Observer) (*Response, error) {
	var lastErr error
	attempts := c.MaxAttempts
	if attempts <= 0 {
		attempts = 1
	}
	callTimeouts := 0
	for attempt := 1; attempt <= attempts; attempt++ {
		start := time.Now()
		callCtx := ctx
		if c.CallTimeout > 0 {
			var cancel context.CancelFunc
			callCtx, cancel = context.WithTimeout(ctx, c.CallTimeout)
			defer cancel()
		}
		// In this order on every call: a model found to take no pictures
		// was found so on a request that already had these notes, and
		// the next request must render the same.
		if hasImages(req) {
			req = c.images.leaveOut(fitImages(req, c.Endpoint.Protocol))
		}
		if c.rejectsImages.Load() && hasImages(req) {
			req = withoutImages(req)
		}
		resp, err := c.once(callCtx, req, obs)
		var ae *APIError
		if err != nil && hasImages(req) && errors.As(err, &ae) && (ae.RejectsImages() || ae.Status == 413) {
			if ae.badImage() || ae.Status == 413 {
				// A picture was refused (undecodable, too big for a limit
				// of this provider's own): go on without it, and say why.
				// The model can see. The one refused is left out of later
				// calls too; the ones taken before stay.
				if c.OnRetry != nil {
					c.OnRetry(attempt, fmt.Errorf("%s refused a picture (%s); sending the text in its place", c.Endpoint.Model, shortBody(ae.Body, 160)), 0)
				}
				c.images.refuse(req, false)
				req = c.images.leaveOut(req)
				obs.reset()
				resp, err = c.once(callCtx, req, obs)
				if err != nil && hasImages(req) && errors.As(err, &ae) && (ae.RejectsImages() || ae.Status == 413) {
					// Still refused over pictures: leave them all out.
					c.images.refuse(req, true)
					req = c.images.leaveOut(req)
					obs.reset()
					resp, err = c.once(callCtx, req, obs)
				}
			} else {
				if c.rejectsImages.CompareAndSwap(false, true) && c.OnRetry != nil {
					// A text-only model: say so once and go on without
					// pictures, on this call and every later one through
					// this client.
					c.OnRetry(attempt, fmt.Errorf("%s does not accept images; sending the text alone", c.Endpoint.Model), 0)
				}
				req = withoutImages(req)
				obs.reset()
				resp, err = c.once(callCtx, req, obs)
			}
		}
		if err == nil {
			c.images.accept(imagePaths(req))
			resp.RefusedImages = c.images.report()
			resp.Elapsed = time.Since(start)
			resp.Protocol = c.Endpoint.Protocol
			resp.BaseURL = c.Endpoint.BaseURL
			resp.Asked = c.Endpoint.Model
			if resp.Model == "" {
				resp.Model = c.Endpoint.Model
			}
			return resp, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, context.DeadlineExceeded) && callCtx.Err() != nil {
			// The whole call ran out its limit. One more try can help (a
			// stuck upstream); five more only hold the turn for hours.
			callTimeouts++
		}
		if !retryable(err) || attempt == attempts || callTimeouts > 1 {
			return nil, err
		}
		wait := backoff(attempt, err)
		if c.OnRetry != nil {
			c.OnRetry(attempt, err, wait)
		}
		obs.reset()
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return nil, lastErr
}

func retryable(err error) bool {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Retryable()
	}
	if errors.Is(err, ErrIdle) || errors.Is(err, ErrTruncatedStream) {
		return true
	}
	if errors.Is(err, context.DeadlineExceeded) || netx.Transient(err) {
		return true
	}
	// Network-level failures (reset, EOF mid-stream, DNS) are transient.
	// Every *url.Error has a Timeout method, so ask it rather than match the
	// type: a bad certificate or a redirect loop will not get better.
	var se interface{ Timeout() bool }
	if errors.As(err, &se) && se.Timeout() {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "EOF") || strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "broken pipe") || strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "TLS handshake") || strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "stream error") || strings.Contains(msg, "unexpected end") ||
		// HTTP/2 teardowns on the long-lived connections to the gateways: a
		// rolling deploy (GOAWAY), a lost keepalive, a closed connection.
		strings.Contains(msg, "GOAWAY") || strings.Contains(msg, "connection lost") ||
		strings.Contains(msg, "conn is closed") || strings.Contains(msg, "conn not usable") ||
		strings.Contains(msg, "use of closed network connection")
}

// shortBody trims an error body to one line of at most n characters.
func shortBody(b string, n int) string {
	b = strings.Join(strings.Fields(b), " ")
	if len(b) > n {
		return b[:n-1] + "…"
	}
	return b
}

func backoff(attempt int, err error) time.Duration {
	var ae *APIError
	if errors.As(err, &ae) && ae.RetryAfter > 0 {
		return min(ae.RetryAfter, 120*time.Second)
	}
	if errors.As(err, &ae) && ae.Status == 429 {
		// Rate limits deserve a longer pause.
		return time.Duration(min(60, 5*attempt*attempt)) * time.Second
	}
	d := time.Duration(1<<uint(attempt-1)) * time.Second
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	return d
}

func (c *Client) once(ctx context.Context, req Request, obs *Observer) (*Response, error) {
	switch c.Endpoint.Protocol {
	case ProtocolResponses:
		return c.responses(ctx, req, obs)
	case ProtocolChat:
		return c.chat(ctx, req, obs)
	case ProtocolAnthropic:
		return c.anthropic(ctx, req, obs)
	default:
		return nil, fmt.Errorf("unknown protocol %q", c.Endpoint.Protocol)
	}
}

// maxTokens picks the request's limit, then the endpoint's, then a default.
func (c *Client) maxTokens(req Request) int {
	if req.MaxTokens > 0 {
		return req.MaxTokens
	}
	if c.Endpoint.MaxTokens > 0 {
		return c.Endpoint.MaxTokens
	}
	return 16384
}

// EstimateTokens is a cheap upper-ish bound used before the provider has
// reported usage for a context (about 3.5 bytes per token for code+prose).
func EstimateTokens(s string) int {
	return len(s)*2/7 + 1
}
