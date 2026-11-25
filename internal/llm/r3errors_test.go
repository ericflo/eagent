package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// streamFailing serves the given frames for every request and counts posts.
func streamFailing(t *testing.T, frames ...string) (*httptest.Server, *atomic.Int32) {
	var posts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		for _, f := range frames {
			fmt.Fprintf(w, "data: %s\n\n", f)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &posts
}

func failingCall(t *testing.T, protocol string, frames ...string) (*APIError, int) {
	t.Helper()
	srv, posts := streamFailing(t, frames...)
	c := NewClient(Endpoint{Protocol: protocol, BaseURL: srv.URL, Model: "m", APIKey: "k"})
	c.MaxAttempts = 3
	c.OnRetry = func(int, error, time.Duration) {}
	_, err := c.Complete(context.Background(), Request{Messages: []Message{{Role: "user", Text: "hi"}}}, nil)
	var ae *APIError
	if !errors.As(err, &ae) {
		t.Fatalf("error %v is not an APIError", err)
	}
	return ae, int(posts.Load())
}

// An error that arrives inside a stream is judged as the same error would
// be in a plain response: an overflow starts a dossier, missing credits or
// access go to a fallback route, and only a transient failure is retried.
func TestMidStreamErrorsKeepTheProvidersStatus(t *testing.T) {
	type want struct {
		overflow, unroutable, retried bool
	}
	cases := []struct {
		name     string
		protocol string
		frames   []string
		want     want
	}{
		{"chat overflow with a numeric code", ProtocolChat, []string{
			`{"choices":[{"index":0,"delta":{"content":""}}]}`,
			`{"error":{"code":400,"message":"This endpoint's maximum context length is 131072 tokens. However, you requested about 140000 tokens."},"choices":[{"index":0,"delta":{"content":""},"finish_reason":"error"}]}`,
		}, want{overflow: true}},
		{"chat overflow with a string code", ProtocolChat, []string{
			`{"error":{"code":"context_length_exceeded","message":"Input is longer than this model takes."}}`,
		}, want{overflow: true}},
		{"chat out of credits", ProtocolChat, []string{
			`{"error":{"code":402,"message":"This request requires more credits, or fewer max_tokens."}}`,
		}, want{unroutable: true}},
		{"chat forbidden", ProtocolChat, []string{
			`{"error":{"code":403,"message":"Your key cannot use this model."}}`,
		}, want{unroutable: true}},
		{"chat provider hiccup", ProtocolChat, []string{
			`{"error":{"code":"server_error","message":"Provider disconnected unexpectedly"},"choices":[{"index":0,"delta":{"content":""},"finish_reason":"error"}]}`,
		}, want{retried: true}},
		{"chat error with no code", ProtocolChat, []string{
			`{"error":{"message":"upstream went away"}}`,
		}, want{retried: true}},
		{"responses failed over the context window", ProtocolResponses, []string{
			`{"type":"response.created","response":{"status":"in_progress"}}`,
			`{"type":"response.failed","response":{"status":"failed","error":{"code":"context_length_exceeded","message":"Your input exceeds the context window of this model."}}}`,
		}, want{overflow: true}},
		{"responses failed on the server", ProtocolResponses, []string{
			`{"type":"response.failed","response":{"status":"failed","error":{"code":"server_error","message":"The server had an error."}}}`,
		}, want{retried: true}},
		{"responses error event, server error", ProtocolResponses, []string{
			`{"type":"error","code":"server_error","message":"Something went wrong.","param":null}`,
		}, want{retried: true}},
		{"responses error event, out of quota", ProtocolResponses, []string{
			`{"type":"error","code":"insufficient_quota","message":"You exceeded your current quota.","param":null}`,
		}, want{unroutable: true}},
		{"anthropic prompt too long", ProtocolAnthropic, []string{
			`{"type":"message_start","message":{"model":"claude","usage":{"input_tokens":1}}}`,
			`{"type":"error","error":{"type":"invalid_request_error","message":"prompt is too long: 210000 tokens > 200000 maximum"}}`,
		}, want{overflow: true}},
		{"anthropic overloaded", ProtocolAnthropic, []string{
			`{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`,
		}, want{retried: true}},
		{"anthropic api error", ProtocolAnthropic, []string{
			`{"type":"error","error":{"type":"api_error","message":"Internal server error"}}`,
		}, want{retried: true}},
		{"anthropic billing", ProtocolAnthropic, []string{
			`{"type":"error","error":{"type":"billing_error","message":"Your credit balance is too low."}}`,
		}, want{unroutable: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ae, posts := failingCall(t, tc.protocol, tc.frames...)
			if ae.ContextOverflow() != tc.want.overflow || ae.Unroutable() != tc.want.unroutable {
				t.Errorf("%v: overflow=%v unroutable=%v, want %v and %v", ae, ae.ContextOverflow(), ae.Unroutable(), tc.want.overflow, tc.want.unroutable)
			}
			if wantPosts := map[bool]int{true: 3, false: 1}[tc.want.retried]; posts != wantPosts {
				t.Errorf("%v: %d posts, want %d", ae, posts, wantPosts)
			}
		})
	}
}

// A rate limit inside a stream is a 429 (retried, with a longer pause),
// whichever provider sends it.
func TestMidStreamRateLimitsAre429(t *testing.T) {
	for _, e := range []*APIError{
		streamError("rate_limit_exceeded", "", "Slow down.", nil),
		streamError(429, "", "Rate limit exceeded", nil),
		streamError(nil, "rate_limit_error", "Number of request tokens has exceeded your per-minute rate limit", nil),
	} {
		if e.Status != 429 || !e.Retryable() || e.QuotaExhausted() {
			t.Errorf("%v: status %d", e, e.Status)
		}
	}
}

// The providers' own wordings for a prompt over the context window and for
// an account out of credits, and errors that only look like them.
func TestOverflowAndQuotaWordings(t *testing.T) {
	overflow := []APIError{
		{Status: 400, Body: `{"type":"error","error":{"type":"invalid_request_error","message":"input length and ` + "`max_tokens`" + ` exceed context limit: 188240 + 21333 > 200000, decrease input length or ` + "`max_tokens`" + ` and try again"}}`},
		{Status: 413, Body: `{"type":"error","error":{"type":"request_too_large","message":"Request exceeds the maximum allowed number of bytes."}}`},
		{Status: 400, Body: `{"error":{"message":"prompt is too long: 1048577 tokens > 1048576 maximum","type":"invalid_request_error"}}`},
	}
	for _, e := range overflow {
		if !e.ContextOverflow() {
			t.Errorf("not taken for an overflow: %s", e.Body)
		}
	}
	notOverflow := []APIError{
		{Status: 400, Body: `{"type":"error","error":{"type":"invalid_request_error","message":"max_tokens: 200000 > 128000, which is the maximum allowed number of output tokens for claude-opus-5-5"}}`},
		{Status: 400, Body: `{"type":"error","error":{"type":"invalid_request_error","message":"messages.3.content.0: text content blocks must be non-empty"}}`},
		{Status: 400, Body: `{"type":"error","error":{"type":"invalid_request_error","message":"messages.1.content.1.image.source.base64: image exceeds 5 MB maximum: 6291456 bytes > 5242880 bytes"}}`},
		{Status: 413, Body: `payload too large`},
	}
	for _, e := range notOverflow {
		if e.ContextOverflow() {
			t.Errorf("taken for an overflow: %s", e.Body)
		}
	}
	quota := []APIError{
		{Status: 402, Body: `{"error":{"message":"This request requires more credits, or fewer max_tokens. You requested up to 32768 tokens, but can only afford 1200.","code":402}}`},
		{Status: 402, Body: `{"type":"error","error":{"type":"billing_error","message":"Your account has a billing problem."}}`, Code: "billing_error"},
	}
	for _, e := range quota {
		if !e.QuotaExhausted() || e.Retryable() || !e.Unroutable() {
			t.Errorf("not taken for missing credits: %s", e.Body)
		}
	}
	if (&APIError{Status: 429, Body: `{"error":{"message":"Rate limit reached for requests"}}`}).QuotaExhausted() {
		t.Error("a rate limit taken for missing credits")
	}
}
