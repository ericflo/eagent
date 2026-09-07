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
