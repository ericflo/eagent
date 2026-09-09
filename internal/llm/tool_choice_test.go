package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A model that only accepts the default tool choice is asked without one,
// on that call and every later one, instead of failing every narrator turn.
func TestToolChoiceRefusalIsRemembered(t *testing.T) {
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(raw))
		if strings.Contains(string(raw), `"tool_choice"`) {
			w.WriteHeader(400)
			fmt.Fprint(w, `{"error":{"code":null,"message":"only \"auto\" is supported for tool_choice. \"none\", \"required\", and named function choices are not currently supported","param":"tool_choice"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "c1", "function": map[string]any{"name": "hold", "arguments": "{}"}}}}}}})
		fmt.Fprintf(w, "data: %s\n\n", chunk)
		fin, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "tool_calls"}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", fin)
	}))
	defer srv.Close()
	c := NewClient(Endpoint{Protocol: ProtocolChat, BaseURL: srv.URL, Model: "muse", APIKey: "k"})
	c.MaxAttempts = 1
	var notes []string
	c.OnRetry = func(attempt int, err error, wait time.Duration) { notes = append(notes, err.Error()) }
	req := Request{Messages: []Message{{Role: "user", Text: "hi"}}, Tools: []Tool{{Name: "hold", Parameters: json.RawMessage(`{"type":"object"}`)}}, ToolChoice: "required"}
	for i := 0; i < 2; i++ {
		resp, err := c.Complete(context.Background(), req, nil)
		if err != nil || len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "hold" {
			t.Fatalf("call %d: resp=%+v err=%v", i, resp, err)
		}
	}
	if len(bodies) != 3 || !strings.Contains(bodies[0], `"tool_choice":"required"`) || strings.Contains(bodies[1], "tool_choice") || strings.Contains(bodies[2], "tool_choice") {
		t.Fatalf("bodies = %q", bodies)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], `does not accept tool_choice "required"`) {
		t.Fatalf("notes = %q", notes)
	}
	if !c.rejectsToolChoice.Load() {
		t.Fatal("refusal not remembered")
	}
	// An unrelated 400 is not mistaken for a tool-choice refusal.
	if (&APIError{Status: 400, Body: `{"error":{"message":"invalid model"}}`}).RejectsToolChoice() {
		t.Fatal("unrelated failure treated as a tool-choice refusal")
	}
}
