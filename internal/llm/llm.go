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
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

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
	MaxTokens       int    `json:"max_tokens,omitempty"`
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

	// CacheBreak marks the last stable message (Anthropic cache_control).
	CacheBreak bool

	// Images are pictures attached to a user message (screenshots the user
	// sent). Adapters inline them as base64 for models that accept images;
	// a model that rejects them gets the text alone.
	Images []Image
}

// Image is a picture on disk to show the model.
type Image struct {
	Path      string
	MediaType string // image/png, image/jpeg, image/gif, image/webp
}

// maxInlineImage caps what is inlined into a request.
const maxInlineImage = 10 << 20

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
	if e.Status != 400 && e.Status != 422 {
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

// ToolResult is the outcome of one tool call.
type ToolResult struct {
	CallID  string
	Name    string
	Output  string
	IsError bool
}

// Request is one model call.
type Request struct {
	System     string
	Messages   []Message
	Tools      []Tool
	ToolChoice string // "" (auto) | "required" | "none"
	MaxTokens  int
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
	Model     string
	Usage     event.Usage
	Stop      string // "stop" | "tool_calls" | "length" | other provider value
	Elapsed   time.Duration
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
	anthropicRetried  atomic.Bool
}

// NewClient returns a client with sensible defaults.
func NewClient(ep Endpoint) *Client {
	return &Client{
		Endpoint:    ep,
		HTTP:        &http.Client{Timeout: 0}, // per-request deadlines are set via context
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
	return e.Code == "insufficient_quota" || e.Code == "credit_balance_exhausted" ||
		strings.Contains(b, "insufficient_quota") || strings.Contains(b, "no credits") ||
		strings.Contains(b, "credit balance") || strings.Contains(b, "insufficient credits")
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
	return strings.Contains(b, "context_length") || strings.Contains(b, "context length") ||
		strings.Contains(b, "maximum context") || strings.Contains(b, "too many tokens") ||
		strings.Contains(b, "prompt is too long") || strings.Contains(b, "exceeds the limit") ||
		strings.Contains(b, "input tokens exceed")
}

// ErrIdle is returned when a stream stalls.
var ErrIdle = errors.New("stream idle timeout")

// ErrTruncatedStream is returned when a stream closes before the provider
// signalled completion.
var ErrTruncatedStream = errors.New("stream ended before completion")

// Complete performs one model call with retries on transient failures.
func (c *Client) Complete(ctx context.Context, req Request, obs *Observer) (*Response, error) {
	var lastErr error
	attempts := c.MaxAttempts
	if attempts <= 0 {
		attempts = 1
	}
	for attempt := 1; attempt <= attempts; attempt++ {
		start := time.Now()
		callCtx := ctx
		if c.CallTimeout > 0 {
			var cancel context.CancelFunc
			callCtx, cancel = context.WithTimeout(ctx, c.CallTimeout)
			defer cancel()
		}
		resp, err := c.once(callCtx, req, obs)
		if err != nil && hasImages(req) {
			var ae *APIError
			if errors.As(err, &ae) && ae.RejectsImages() {
				// A text-only model: say so once and go on without pictures.
				if c.OnRetry != nil {
					c.OnRetry(attempt, fmt.Errorf("%s does not accept images; sending the text alone", c.Endpoint.Model), 0)
				}
				req = withoutImages(req)
				obs.reset()
				resp, err = c.once(callCtx, req, obs)
			}
		}
		if err == nil {
			resp.Elapsed = time.Since(start)
			resp.Protocol = c.Endpoint.Protocol
			if resp.Model == "" {
				resp.Model = c.Endpoint.Model
			}
			return resp, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if !retryable(err) || attempt == attempts {
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
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	// Network-level failures (reset, EOF mid-stream, DNS) are transient.
	var se interface{ Timeout() bool }
	if errors.As(err, &se) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "EOF") || strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "broken pipe") || strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "TLS handshake") || strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "stream error") || strings.Contains(msg, "unexpected end")
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
