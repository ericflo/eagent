package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// A Chat Completions endpoint that refuses prompt_cache_key and also takes
// no pictures. The key is dropped, the retry is refused over the picture,
// and that refusal is the one Complete must see, so that it goes on with
// the text alone. The key stays dropped: the retry was not refused over it.
func TestADroppedCacheKeyDoesNotHideAPictureRefusal(t *testing.T) {
	dir := t.TempDir()
	pic := filepath.Join(dir, "shot.png")
	if err := os.WriteFile(pic, pngOf(t, 4, 4), 0o644); err != nil {
		t.Fatal(err)
	}
	srv, _ := chatRefusing(t, func(req map[string]any) string {
		if req["prompt_cache_key"] != nil {
			return `{"error":{"message":"Unrecognized request argument supplied: prompt_cache_key"}}`
		}
		raw, _ := json.Marshal(req)
		if strings.Contains(string(raw), "image_url") {
			return `{"error":{"message":"This model does not support image input."}}`
		}
		return ""
	})
	c := NewClient(Endpoint{Protocol: ProtocolChat, BaseURL: srv.URL, Model: "m", APIKey: "k"})
	c.MaxAttempts = 1
	req := Request{CacheKey: "sess", Messages: []Message{{Role: "user", Text: "look", Images: []Image{{Path: pic, MediaType: "image/png"}}}}}
	if _, err := c.Complete(context.Background(), req, nil); err != nil {
		t.Fatalf("a text-only model behind an endpoint that refuses prompt_cache_key now fails the call: %v", err)
	}
}

// The same endpoint with a conversation too long for the model: the
// overflow is what comes back, so the harness can restart from a dossier.
func TestADroppedCacheKeyDoesNotHideAnOverflow(t *testing.T) {
	srv, _ := chatRefusing(t, func(req map[string]any) string {
		if req["prompt_cache_key"] != nil {
			return `{"error":{"message":"Unrecognized request argument supplied: prompt_cache_key"}}`
		}
		return `{"error":{"message":"This model's maximum context length is 8192 tokens. However, your messages resulted in 9000 tokens."}}`
	})
	c := NewClient(Endpoint{Protocol: ProtocolChat, BaseURL: srv.URL, Model: "m", APIKey: "k"})
	c.MaxAttempts = 1
	_, err := c.Complete(context.Background(), Request{CacheKey: "sess", Messages: []Message{{Role: "user", Text: "hi"}}}, nil)
	var ae *APIError
	if !errors.As(err, &ae) || !ae.ContextOverflow() {
		t.Fatalf("the overflow is not reported as one: %v", err)
	}
}

// A client that has seen no call go through (the session was resumed, or
// its route rebuilt) still knows which pictures the provider took: those
// that stand before the model's last reply. A refusal is about what came
// after it, so the new client sends the same request the long-lived one
// does and the pictures taken before stay in.
func TestANewClientBlamesOnlyThePictureAfterTheLastReply(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.png")
	b := filepath.Join(dir, "b.png")
	if err := os.WriteFile(a, pngOf(t, 10, 10), 0o644); err != nil {
		t.Fatal(err)
	}
	bad := append(pngOf(t, 11, 11), []byte("trailing bytes the provider dislikes")...)
	if err := os.WriteFile(b, bad, 0o644); err != nil {
		t.Fatal(err)
	}
	srv := newImageServer(t, 400, `{"type":"error","error":{"type":"invalid_request_error","message":"Could not process image"}}`, base64.StdEncoding.EncodeToString(bad))
	first := Request{Messages: []Message{{Role: "user", Text: "one", Images: []Image{{Path: a, MediaType: "image/png"}}}}}
	second := Request{Messages: []Message{
		{Role: "user", Text: "one", Images: []Image{{Path: a, MediaType: "image/png"}}},
		{Role: "assistant", Text: "seen"},
		{Role: "user", Text: "two", Images: []Image{{Path: b, MediaType: "image/png"}}},
	}}
	aData := base64.StdEncoding.EncodeToString(pngOf(t, 10, 10))

	lived := NewClient(Endpoint{Protocol: ProtocolAnthropic, BaseURL: srv.srv.URL, Model: "claude-x", APIKey: "k"})
	lived.MaxAttempts = 1
	if _, err := lived.Complete(context.Background(), first, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := lived.Complete(context.Background(), second, nil); err != nil {
		t.Fatal(err)
	}
	srv.mu.Lock()
	livedLast := srv.bodies[len(srv.bodies)-1]
	srv.mu.Unlock()
	if !strings.Contains(livedLast, aData) {
		t.Fatalf("the long-lived client left out the picture it had sent before")
	}

	restarted := NewClient(Endpoint{Protocol: ProtocolAnthropic, BaseURL: srv.srv.URL, Model: "claude-x", APIKey: "k"})
	restarted.MaxAttempts = 1
	if _, err := restarted.Complete(context.Background(), second, nil); err != nil {
		t.Fatal(err)
	}
	srv.mu.Lock()
	restartedLast := srv.bodies[len(srv.bodies)-1]
	srv.mu.Unlock()
	if restartedLast != livedLast {
		t.Fatalf("after a restart the same record renders another request: picture a.png sent %v (before the restart: true)", strings.Contains(restartedLast, aData))
	}
}

// A picture over 10 MB on disk is never sent, and nothing is written in
// its place: a turn that held one renders the bytes it always has (here
// the Chat Completions form, a parts array holding the text alone).
func TestAnOversizePictureInAnOldTurnRendersAsItDid(t *testing.T) {
	dir := t.TempDir()
	huge := filepath.Join(dir, "huge.png")
	if err := os.WriteFile(huge, make([]byte, maxInlineImage+1), 0o644); err != nil {
		t.Fatal(err)
	}
	srv, reqs := chatRefusing(t, func(map[string]any) string { return "" })
	c := NewClient(Endpoint{Protocol: ProtocolChat, BaseURL: srv.URL, Model: "m", APIKey: "k"})
	c.MaxAttempts = 1
	req := Request{Messages: []Message{{Role: "user", Text: "look at this", Images: []Image{{Path: huge, MediaType: "image/png"}}}}}
	if _, err := c.Complete(context.Background(), req, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal((*reqs)[0]["messages"].([]any)[0].(map[string]any)["content"])
	want := `[{"text":"look at this","type":"text"}]`
	if string(got) != want {
		t.Fatalf("an old turn with an oversize picture renders differently from b0bfe64:\n got %s\nwant %s", got, want)
	}
}

// A local Responses server (LM Studio) returns its reasoning as plain
// reasoning_text content with no encrypted_content. Such an item carries
// its reasoning itself, so it goes back as it came and the turn is not
// rebuilt.
func TestReasoningALocalServerReturnsInTheItemStillReplays(t *testing.T) {
	native := json.RawMessage(`[{"type":"reasoning","id":"rs_1","summary":[],"content":[{"type":"reasoning_text","text":"plan A"}]},{"type":"function_call","id":"fc_1","call_id":"call_1","name":"ping","arguments":"{}"}]`)
	host := "http://127.0.0.1:1234/v1"
	req := Request{Messages: []Message{
		{Role: "user", Text: "go"},
		{Role: "assistant", ToolCalls: []event.ToolCall{{ID: "call_1", Name: "ping", Args: json.RawMessage(`{}`)}},
			Native: native, NativeProtocol: ProtocolResponses, NativeHost: host, NativeModel: "openai/gpt-oss-20b"},
		{Role: "tool", Results: []ToolResult{{CallID: "call_1", Name: "ping", Output: "pong"}}},
	}}
	items := responsesInputFor(req, true, host, "openai/gpt-oss-20b")
	raw, _ := json.Marshal(items)
	if !strings.Contains(string(raw), `"plan A"`) || !strings.Contains(string(raw), `"rs_1"`) {
		t.Fatalf("the model's own reasoning_text is no longer replayed to it (b0bfe64 sent it):\n%s", raw)
	}
}

// A refusal is reported to the caller, who writes it down and marks the
// picture from then on. A marked picture is never uploaded, by any client,
// and the request a new client then sends is the one the first client's
// retry sent, byte for byte.
func TestARefusedPictureIsReportedAndStaysOutForAnyClient(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.png")
	b := filepath.Join(dir, "b.png")
	if err := os.WriteFile(a, pngOf(t, 10, 10), 0o644); err != nil {
		t.Fatal(err)
	}
	bad := append(pngOf(t, 11, 11), []byte("trailing bytes the provider dislikes")...)
	if err := os.WriteFile(b, bad, 0o644); err != nil {
		t.Fatal(err)
	}
	badData := base64.StdEncoding.EncodeToString(bad)
	srv := newImageServer(t, 400, `{"type":"error","error":{"type":"invalid_request_error","message":"Could not process image"}}`, badData)
	conv := func(refused bool) Request {
		return Request{Messages: []Message{
			{Role: "user", Text: "one", Images: []Image{{Path: a, MediaType: "image/png"}}},
			{Role: "assistant", Text: "seen"},
			{Role: "user", Text: "two", Images: []Image{{Path: b, MediaType: "image/png", Refused: refused}}},
		}}
	}
	first := NewClient(Endpoint{Protocol: ProtocolAnthropic, BaseURL: srv.srv.URL, Model: "claude-x", APIKey: "k"})
	first.MaxAttempts = 1
	resp, err := first.Complete(context.Background(), conv(false), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.RefusedImages) != 1 || resp.RefusedImages[0] != b {
		t.Fatalf("the call reported %q as refused, want %q", resp.RefusedImages, b)
	}
	srv.mu.Lock()
	sent, retry := len(srv.bodies), srv.bodies[len(srv.bodies)-1]
	srv.mu.Unlock()

	// Another client (the session resumed, or its route changed), given
	// the conversation as the record now renders it.
	second := NewClient(Endpoint{Protocol: ProtocolAnthropic, BaseURL: srv.srv.URL, Model: "claude-x", APIKey: "k"})
	second.MaxAttempts = 1
	resp, err = second.Complete(context.Background(), conv(true), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.RefusedImages) != 0 {
		t.Fatalf("a picture already written down as refused was reported again: %q", resp.RefusedImages)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if n := len(srv.bodies) - sent; n != 1 {
		t.Fatalf("the new client made %d requests, want one (nothing uploaded to be refused)", n)
	}
	if got := srv.bodies[len(srv.bodies)-1]; got != retry {
		t.Fatalf("the new client's request is not the one the first client's retry sent:\n got %s\nwant %s", got, retry)
	}
	if strings.Contains(retry, badData) || !strings.Contains(retry, base64.StdEncoding.EncodeToString(pngOf(t, 10, 10))) {
		t.Fatal("the retry should carry the picture taken before and not the refused one")
	}
}
