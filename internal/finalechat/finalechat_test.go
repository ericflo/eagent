package finalechat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePrefersEnvThenConfigFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("FINALECHAT_TOKEN", "")
	t.Setenv("FINALECHAT_URL", "")
	if _, ok := Resolve("", ""); ok {
		t.Fatal("resolved a token from nowhere")
	}
	dir := filepath.Join(home, ".config", "finalechat")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"token":"fc_file","base_url":"https://example.test"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, ok := Resolve("", "")
	if !ok || c.Token != "fc_file" || c.BaseURL != "https://example.test" || c.Source != "~/.config/finalechat/config.json" {
		t.Fatalf("file resolve = %+v ok=%v", c, ok)
	}
	t.Setenv("MY_FC", "fc_env")
	c, ok = Resolve("MY_FC", "")
	if !ok || c.Token != "fc_env" || c.Source != "$MY_FC" || c.BaseURL != "https://example.test" {
		t.Fatalf("env resolve = %+v ok=%v", c, ok)
	}
	c, _ = Resolve("MY_FC", "https://override.test")
	if c.BaseURL != "https://override.test" {
		t.Fatalf("base url override = %s", c.BaseURL)
	}
}

func TestClientCallsAndErrors(t *testing.T) {
	var gotAuth, gotPath, gotQuery string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotBody = nil
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/me":
			_, _ = w.Write([]byte(`{"auth":"token","base_url":"x","push_enabled":true,"version":"v","user":{"display_name":"Eric","email":"e@x","settings":{"notify_all_messages":false,"remote_mode":true}}}`))
		case r.URL.Path == "/api/v1/threads/ext:eagent:1/messages" && r.Method == http.MethodPost:
			w.WriteHeader(201)
			_, _ = w.Write([]byte(`{"message":{"id":"m1","thread_id":"t1","sender":"agent","body":"hi","format":"markdown","importance":"important","meta":{},"created_at":"2026-09-07T00:00:00Z"},"thread":{"id":"t1","external_id":"eagent:1","title":"proj","agent":"eagent"}}`))
		case r.URL.Path == "/api/v1/threads/ext:eagent:1/messages" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"messages":[{"id":"m2","thread_id":"t1","sender":"user","body":"Beta — note","format":"text","importance":"normal","meta":{"kind":"answer","question_id":"q9"},"created_at":"2026-09-07T00:00:01Z"}],"has_more":false,"thread_id":"t1","timed_out":false}`))
		case r.URL.Path == "/api/v1/threads/ext:eagent:1/questions":
			w.WriteHeader(201)
			_, _ = w.Write([]byte(`{"question":{"id":"q9","thread_id":"t1","prompt":"Which?","options":[{"label":"A"}],"allow_freeform":true,"multi_select":false,"status":"pending","answer":null,"meta":{},"created_at":"2026-09-07T00:00:00Z","expires_at":null},"thread":{"id":"t1"}}`))
		case r.URL.Path == "/api/v1/questions/q9/cancel":
			w.WriteHeader(409)
			_, _ = w.Write([]byte(`{"error":{"code":"already_resolved","message":"done"}}`))
		case r.URL.Path == "/api/v1/questions/bad/cancel":
			w.WriteHeader(422)
			_, _ = w.Write([]byte(`{"error":{"code":"validation_failed","message":"no such question"}}`))
		default:
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"nope"}}`))
		}
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Token: "fc_test"}
	ctx := context.Background()

	me, err := c.Me(ctx)
	if err != nil || me.User.DisplayName != "Eric" || !me.User.Settings.RemoteMode {
		t.Fatalf("me = %+v err=%v", me, err)
	}
	if gotAuth != "Bearer fc_test" {
		t.Fatalf("auth header = %q", gotAuth)
	}

	msg, thread, err := c.Post(ctx, Ref("eagent:1"), PostRequest{Body: "hi", Importance: "important", Title: "proj", Agent: "eagent", Notify: boolp(false)})
	if err != nil || msg.ID != "m1" || thread.ID != "t1" {
		t.Fatalf("post = %+v %+v err=%v", msg, thread, err)
	}
	if gotBody["title"] != "proj" || gotBody["notify"] != false || gotBody["importance"] != "important" {
		t.Fatalf("post body = %v", gotBody)
	}

	q, _, err := c.Ask(ctx, Ref("eagent:1"), AskRequest{Prompt: "Which?", Options: []Option{{Label: "A"}}, TimeoutSeconds: 60})
	if err != nil || q.ID != "q9" || q.Status != "pending" {
		t.Fatalf("ask = %+v err=%v", q, err)
	}
	if gotBody["timeout_seconds"] != float64(60) {
		t.Fatalf("ask body = %v", gotBody)
	}

	msgs, timedOut, err := c.Messages(ctx, Ref("eagent:1"), "m1", "user", 5, 50)
	if err != nil || len(msgs) != 1 || timedOut {
		t.Fatalf("messages = %+v timed_out=%v err=%v", msgs, timedOut, err)
	}
	if gotQuery != "after=m1&limit=50&sender=user&wait=5" {
		t.Fatalf("query = %s", gotQuery)
	}
	if id, ok := msgs[0].IsAnswer(); !ok || id != "q9" {
		t.Fatalf("answer detection = %s %v", id, ok)
	}
	if gotPath != "/api/v1/threads/ext:eagent:1/messages" {
		t.Fatalf("path = %s", gotPath)
	}

	if err := c.Cancel(ctx, "q9"); err != nil {
		t.Fatalf("cancelling a resolved question should be fine: %v", err)
	}
	err = c.Cancel(ctx, "bad")
	var e *Error
	if err == nil || !asError(err, &e) || e.Code != "validation_failed" || e.Status != 422 {
		t.Fatalf("error envelope = %v", err)
	}
	if _, err := c.Thread(ctx, "missing"); err == nil {
		t.Fatal("404 should be an error")
	}
}

func boolp(b bool) *bool { return &b }

func asError(err error, target **Error) bool {
	e, ok := err.(*Error)
	if ok {
		*target = e
	}
	return ok
}
