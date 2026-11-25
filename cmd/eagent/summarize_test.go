package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/store"
)

// `eagent summarize` moves to the configured fallback when the first route
// cannot serve the model, as a session does.
func TestSummarizeUsesTheFallback(t *testing.T) {
	for _, n := range []string{"EAGENT_PRESET", "EAGENT_CONFIG", "EAGENT_MODEL", "EAGENT_BASE_URL", "EAGENT_PROTOCOL", "EAGENT_SUMMARIZER_MODE", "EAGENT_SUMMARIZER_MODEL"} {
		t.Setenv(n, "")
	}
	t.Setenv("EAGENT_FINALECHAT", "off")
	var refused, served atomic.Int32
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		refused.Add(1)
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"error":{"message":"no access to this model"}}`)
	}))
	defer down.Close()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": `{"title": "Fix the login page", "summary": "The login page was fixed."}`}}}})
		fmt.Fprintf(w, "data: %s\n\n", chunk)
		chunk, _ = json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
	}))
	defer up.Close()
	project := t.TempDir()
	if err := os.MkdirAll(project+"/.agents/eagent", 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := fmt.Sprintf(`{"model":{"protocol":"openai-chat","base_url":%q,"model":"m","api_key_env":"","reasoning_effort":"","fallback":{"protocol":"openai-chat","base_url":%q,"model":"m","api_key_env":""}}}`, down.URL+"/v1", up.URL+"/v1")
	if err := os.WriteFile(config.File(project), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(store.Root(project), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.Append(event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{})); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.Append(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "fix the login page"})); err != nil {
		t.Fatal(err)
	}
	if code := cmdSummarize(project, "", "", []string{sess.ID}, false, true, true, false); code != 0 {
		t.Fatalf("exit code %d (refused %d, served %d)", code, refused.Load(), served.Load())
	}
	if refused.Load() == 0 || served.Load() == 0 {
		t.Fatalf("refused %d, served %d", refused.Load(), served.Load())
	}
}
