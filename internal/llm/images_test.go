package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestImagesAreInlinedAndDroppedForTextOnlyModels(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "shot.png")
	if err := os.WriteFile(img, []byte("\x89PNG\r\n\x1a\nfake"), 0o644); err != nil {
		t.Fatal(err)
	}
	req := Request{Messages: []Message{{Role: "user", Text: "look", Images: []Image{{Path: img, MediaType: "image/png"}}}}}

	// Chat completions: a content array with an image_url data URL.
	msgs := chatMessages(req)
	parts, ok := msgs[0]["content"].([]map[string]any)
	if !ok || len(parts) != 2 || parts[1]["type"] != "image_url" || !strings.HasPrefix(parts[1]["image_url"].(map[string]any)["url"].(string), "data:image/png;base64,") {
		t.Fatalf("chat content = %v", msgs[0]["content"])
	}
	// Responses: input_text + input_image.
	items := responsesInput(req, false)
	rp := items[0].(map[string]any)["content"].([]map[string]any)
	if len(rp) != 2 || rp[1]["type"] != "input_image" {
		t.Fatalf("responses content = %v", rp)
	}
	// A missing file is skipped, leaving text only.
	gone := Request{Messages: []Message{{Role: "user", Text: "look", Images: []Image{{Path: filepath.Join(dir, "nope.png")}}}}}
	if parts := chatMessages(gone)[0]["content"].([]map[string]any); len(parts) != 1 {
		t.Fatalf("missing image should be skipped: %v", parts)
	}

	// A model that rejects images gets the text alone on a second try.
	calls := 0
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(raw))
		calls++
		if strings.Contains(string(raw), "image_url") {
			w.WriteHeader(400)
			fmt.Fprint(w, `{"error":{"message":"This model does not support image input"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": "text only"}}}})
		fmt.Fprintf(w, "data: %s\n\n", chunk)
		fin, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", fin)
	}))
	defer srv.Close()
	c := NewClient(Endpoint{Protocol: ProtocolChat, BaseURL: srv.URL, Model: "m", APIKey: "k"})
	c.MaxAttempts = 1
	var noted string
	c.OnRetry = func(attempt int, err error, wait time.Duration) { noted = err.Error() }
	resp, err := c.Complete(context.Background(), req, nil)
	if err != nil || resp.Text != "text only" {
		t.Fatalf("resp=%+v err=%v", resp, err)
	}
	if calls != 2 || !strings.Contains(bodies[1], "could not be shown") || strings.Contains(bodies[1], "image_url") {
		t.Fatalf("calls=%d second body=%s", calls, bodies[1])
	}
	if !strings.Contains(noted, "does not accept images") {
		t.Fatalf("retry note = %q", noted)
	}
}

func TestReasoningItemsAreDroppedUnlessReplayed(t *testing.T) {
	native := json.RawMessage(`[{"type":"reasoning","id":"rs_1","encrypted_content":"xx","summary":[]},{"type":"function_call","id":"fc_1","call_id":"c1","name":"ping","arguments":"{}"}]`)
	req := Request{Messages: []Message{
		{Role: "user", Text: "go"},
		{Role: "assistant", Native: native, NativeProtocol: ProtocolResponses},
		{Role: "tool", Results: []ToolResult{{CallID: "c1", Name: "ping", Output: "pong"}}},
	}}
	types := func(items []any) []string {
		var out []string
		for _, it := range items {
			raw, _ := json.Marshal(it)
			var probe struct {
				Type string `json:"type"`
				Role string `json:"role"`
			}
			_ = json.Unmarshal(raw, &probe)
			if probe.Type == "" {
				probe.Type = probe.Role
			}
			out = append(out, probe.Type)
		}
		return out
	}
	if got := types(responsesInput(req, false)); strings.Join(got, ",") != "user,function_call,function_call_output" {
		t.Fatalf("without replay: %v", got)
	}
	if got := types(responsesInput(req, true)); strings.Join(got, ",") != "user,reasoning,function_call,function_call_output" {
		t.Fatalf("with replay: %v", got)
	}
}

// A model that refused pictures once is not shown them again: later calls
// through the same client send the text alone, in one request.
func TestImageRefusalIsRemembered(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "shot.png")
	if err := os.WriteFile(img, []byte("\x89PNG\r\n\x1a\nfake"), 0o644); err != nil {
		t.Fatal(err)
	}
	posts, withImage := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		posts++
		if strings.Contains(string(raw), "image_url") {
			withImage++
			w.WriteHeader(400)
			fmt.Fprint(w, `{"error":{"message":"This model does not support image input"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": "text only"}}}})
		fmt.Fprintf(w, "data: %s\n\n", chunk)
		fin, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", fin)
	}))
	defer srv.Close()
	c := NewClient(Endpoint{Protocol: ProtocolChat, BaseURL: srv.URL, Model: "text-only", APIKey: "k"})
	c.MaxAttempts = 1
	notes := 0
	c.OnRetry = func(int, error, time.Duration) { notes++ }
	for i := 0; i < 3; i++ {
		req := Request{Messages: []Message{{Role: "user", Text: "look", Images: []Image{{Path: img, MediaType: "image/png"}}}}}
		if _, err := c.Complete(context.Background(), req, nil); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if posts != 4 || withImage != 1 || notes != 1 {
		t.Fatalf("posts=%d withImage=%d notes=%d; want 4, 1, 1", posts, withImage, notes)
	}
}

// anthropicSSE writes one complete streamed Anthropic reply.
func anthropicSSE(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "text/event-stream")
	frames := []string{
		`{"type":"message_start","message":{"model":"claude-test","usage":{"input_tokens":5}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"` + text + `"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`,
		`{"type":"message_stop"}`,
	}
	for _, f := range frames {
		fmt.Fprintf(w, "data: %s\n\n", f)
	}
}

// A model that rejects adaptive thinking is probed once per call, not once
// per client: concurrent workers sharing the client all get their retry, and
// the remembered flavour flips exactly once.
func TestAnthropicThinkingProbeIsPerCall(t *testing.T) {
	var mu sync.Mutex
	posts, rejected := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		posts++
		adaptive := strings.Contains(string(raw), `"adaptive"`)
		if adaptive {
			rejected++
		}
		mu.Unlock()
		if adaptive {
			w.WriteHeader(400)
			fmt.Fprint(w, `{"type":"error","error":{"type":"invalid_request_error","message":"thinking.type: adaptive is not supported by this model"}}`)
			return
		}
		anthropicSSE(w, "ok")
	}))
	defer srv.Close()
	c := NewClient(Endpoint{Protocol: ProtocolAnthropic, BaseURL: srv.URL, Model: "claude-old", APIKey: "k", ReasoningEffort: "medium", MaxTokens: 4096})
	c.MaxAttempts = 1
	const workers = 6
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		go func() {
			_, err := c.Complete(context.Background(), Request{Messages: []Message{{Role: "user", Text: "hi"}}}, nil)
			errs <- err
		}()
	}
	for i := 0; i < workers; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("a worker lost its retry: %v", err)
		}
	}
	if !c.anthropicBudgeted.Load() {
		t.Fatal("the budgeted flavour should be remembered")
	}
	// Once learned, later calls do not probe again.
	mu.Lock()
	before := posts
	mu.Unlock()
	if _, err := c.Complete(context.Background(), Request{Messages: []Message{{Role: "user", Text: "again"}}}, nil); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if posts != before+1 || rejected > workers {
		t.Fatalf("posts=%d (before %d) rejected=%d", posts, before, rejected)
	}
}

// A pydantic-style "does not match any of the expected tags: 'text'" is a
// text-only model, and is remembered as one after the first refusal.
func TestPydanticContentTypeRefusalIsRemembered(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "shot.png")
	if err := os.WriteFile(img, []byte("\x89PNG\r\n\x1a\nfake"), 0o644); err != nil {
		t.Fatal(err)
	}
	posts, withImage := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		posts++
		if strings.Contains(string(raw), "image_url") {
			withImage++
			w.WriteHeader(400)
			fmt.Fprint(w, `{"object":"error","message":"[{'type': 'union_tag_invalid', 'loc': ('body', 'messages', 0, 'content'), 'msg': \"Input tag 'image_url' found using 'type' does not match any of the expected tags: 'text'\"}]"}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": "text only"}}}})
		fmt.Fprintf(w, "data: %s\n\n", chunk)
		fin, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", fin)
	}))
	defer srv.Close()
	c := NewClient(Endpoint{Protocol: ProtocolChat, BaseURL: srv.URL, Model: "vllm-text", APIKey: "k"})
	c.MaxAttempts = 1
	for i := 0; i < 3; i++ {
		req := Request{Messages: []Message{{Role: "user", Text: "look", Images: []Image{{Path: img, MediaType: "image/png"}}}}}
		if _, err := c.Complete(context.Background(), req, nil); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if posts != 4 || withImage != 1 || !c.rejectsImages.Load() {
		t.Fatalf("posts=%d withImage=%d latched=%v; want 4, 1, true", posts, withImage, c.rejectsImages.Load())
	}
	// Anthropic's "image exceeds 5 MB maximum" is about the picture, not the model.
	if (&APIError{Status: 400, Body: `{"error":{"message":"messages.0.content.1.image.source.base64.data: image exceeds 5 MB maximum"}}`}).badImage() != true {
		t.Fatal("an oversized picture must not be remembered as a blind model")
	}
}

// A 400 about a thinking block in the history is not a request for the
// other thinking flavour; and a flavour flip that does not help is undone.
func TestAnthropicFlavourFlipsOnlyForTheParameter(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	mode := "history" // history: the 400 is about messages; both: every flavour is refused
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(raw))
		m := mode
		mu.Unlock()
		switch m {
		case "history":
			w.WriteHeader(400)
			fmt.Fprint(w, `{"type":"error","error":{"type":"invalid_request_error","message":"messages.3: a final assistant message must start with a thinking block"}}`)
		case "both":
			w.WriteHeader(400)
			fmt.Fprint(w, `{"type":"error","error":{"type":"invalid_request_error","message":"thinking.type: not supported"}}`)
		default:
			anthropicSSE(w, "ok")
		}
	}))
	defer srv.Close()
	c := NewClient(Endpoint{Protocol: ProtocolAnthropic, BaseURL: srv.URL, Model: "claude-x", APIKey: "k", ReasoningEffort: "high", MaxTokens: 8192})
	c.MaxAttempts = 1
	req := Request{Messages: []Message{{Role: "user", Text: "hi"}}}
	if _, err := c.Complete(context.Background(), req, nil); err == nil {
		t.Fatal("the history error should surface")
	}
	if c.anthropicBudgeted.Load() {
		t.Fatal("a complaint about the history flipped the thinking flavour")
	}
	mu.Lock()
	if len(bodies) != 1 || !strings.Contains(bodies[0], `"adaptive"`) {
		t.Fatalf("posts = %d %q", len(bodies), bodies)
	}
	mode = "both"
	bodies = nil
	mu.Unlock()
	if _, err := c.Complete(context.Background(), req, nil); err == nil {
		t.Fatal("both flavours refused should surface")
	}
	mu.Lock()
	n := len(bodies)
	mu.Unlock()
	if n != 2 || c.anthropicBudgeted.Load() {
		t.Fatalf("a flip that did not help must be undone: posts=%d budgeted=%v", n, c.anthropicBudgeted.Load())
	}
}

// A thinking budget fits under max_tokens; when there is no room, no
// thinking block is sent rather than one the API rejects.
func TestAnthropicBudgetFitsUnderMaxTokens(t *testing.T) {
	var mu sync.Mutex
	var last string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		last = string(raw)
		mu.Unlock()
		anthropicSSE(w, "ok")
	}))
	defer srv.Close()
	for _, tc := range []struct {
		maxTokens int
		wantBlock bool
		budget    int
	}{{200, false, 0}, {1024, false, 0}, {1500, true, 1024}, {4096, true, 2048}, {60000, true, 24576}} {
		c := NewClient(Endpoint{Protocol: ProtocolAnthropic, BaseURL: srv.URL, Model: "claude-old", APIKey: "k", ReasoningEffort: "high", MaxTokens: tc.maxTokens})
		c.anthropicBudgeted.Store(true)
		c.MaxAttempts = 1
		req := Request{Messages: []Message{{Role: "user", Text: "hi"}}, ToolChoice: "required", Tools: []Tool{{Name: "ping", Description: "Ping.", Parameters: json.RawMessage(`{"type":"object","properties":{}}`)}}}
		if _, err := c.Complete(context.Background(), req, nil); err != nil {
			t.Fatalf("max_tokens %d: %v", tc.maxTokens, err)
		}
		mu.Lock()
		body := last
		mu.Unlock()
		var parsed map[string]any
		_ = json.Unmarshal([]byte(body), &parsed)
		th, has := parsed["thinking"].(map[string]any)
		if has != tc.wantBlock {
			t.Fatalf("max_tokens %d: thinking block present=%v want %v", tc.maxTokens, has, tc.wantBlock)
		}
		if has && int(th["budget_tokens"].(float64)) != tc.budget {
			t.Fatalf("max_tokens %d: budget %v want %d", tc.maxTokens, th["budget_tokens"], tc.budget)
		}
		if _, forced := parsed["tool_choice"]; forced == has {
			t.Fatalf("max_tokens %d: tool_choice must be dropped exactly when thinking is sent", tc.maxTokens)
		}
	}
}
