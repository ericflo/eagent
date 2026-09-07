package finalechat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	// FINALECHAT_URL names a local or staging server and wins over the file.
	t.Setenv("FINALECHAT_URL", "http://127.0.0.1:8787")
	if c, _ = Resolve("MY_FC", ""); c.BaseURL != "http://127.0.0.1:8787" {
		t.Fatalf("env url should beat the file: %s", c.BaseURL)
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

func TestMultipartPostAndDownload(t *testing.T) {
	var gotCT string
	var gotFields map[string]string
	var gotFile struct {
		name, ct string
		data     []byte
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/threads/ext:e/messages" && r.Method == http.MethodPost:
			gotCT = r.Header.Get("Content-Type")
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				w.WriteHeader(400)
				return
			}
			gotFields = map[string]string{}
			for k, v := range r.MultipartForm.Value {
				gotFields[k] = v[0]
			}
			for _, hs := range r.MultipartForm.File {
				h := hs[0]
				f, _ := h.Open()
				gotFile.data, _ = io.ReadAll(f)
				gotFile.name, gotFile.ct = h.Filename, h.Header.Get("Content-Type")
			}
			w.WriteHeader(201)
			_, _ = w.Write([]byte(`{"message":{"id":"m9","thread_id":"t","sender":"agent","body":"x","format":"markdown","importance":"normal","meta":{},"created_at":"2026-09-07T00:00:00Z","attachments":[{"id":"a1","kind":"file","content_type":"text/plain","filename":"notes.txt","size":5,"url":"/api/v1/attachments/a1"}]},"thread":{"id":"t"}}`))
		case r.URL.Path == "/api/v1/attachments/a1":
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("Content-Disposition", `inline; filename="notes.txt"`)
			_, _ = w.Write([]byte("hello"))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Token: "fc_test"}
	msg, _, err := c.Post(context.Background(), Ref("e"), PostRequest{Body: "x", Importance: "important", Notify: boolp(false), Meta: map[string]any{"k": "v"}, Files: []File{{Name: "notes.txt", ContentType: "text/plain", Data: []byte("hello")}}})
	if err != nil || len(msg.Attachments) != 1 || msg.Attachments[0].ID != "a1" {
		t.Fatalf("post = %+v err=%v", msg, err)
	}
	if !strings.HasPrefix(gotCT, "multipart/form-data") || gotFields["body"] != "x" || gotFields["importance"] != "important" || gotFields["notify"] != "false" || gotFields["meta"] != `{"k":"v"}` {
		t.Fatalf("multipart fields = %v (ct %s)", gotFields, gotCT)
	}
	if gotFile.name != "notes.txt" || gotFile.ct != "text/plain" || string(gotFile.data) != "hello" {
		t.Fatalf("file part = %+v", gotFile)
	}
	var buf bytes.Buffer
	ct, name, err := c.Download(context.Background(), msg.Attachments[0].URL, &buf)
	if err != nil || ct != "text/plain" || name != "notes.txt" || buf.String() != "hello" {
		t.Fatalf("download = %q %q %q err=%v", ct, name, buf.String(), err)
	}
	if _, _, err := c.Download(context.Background(), "missing", &buf); err == nil {
		t.Fatal("404 should be an error")
	}
}

// A deployment that lists activity and idempotency: the status line is set
// and cleared, idempotent posts are retried on 5xx and 429 (honouring
// Retry-After), and a post without a key is never retried.
func TestFeaturesActivityAndIdempotentRetry(t *testing.T) {
	old := retrySleep
	var slept []time.Duration
	retrySleep = func(ctx context.Context, d time.Duration) bool { slept = append(slept, d); return true }
	defer func() { retrySleep = old }()
	var mu sync.Mutex
	attempts := map[string]int{}
	var activity []map[string]any
	cleared := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-Id", "req-1")
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.URL.Path == "/api/v1/me":
			_, _ = w.Write([]byte(`{"auth":"token","features":["activity","idempotency","dismiss"],"user":{"settings":{"remote_mode":false}}}`))
		case strings.HasSuffix(r.URL.Path, "/activity") && r.Method == http.MethodPost:
			activity = append(activity, body)
			_, _ = w.Write([]byte(`{"applied":true,"thread":{"id":"t1","activity":{"text":"x","kind":"tool"}}}`))
		case strings.HasSuffix(r.URL.Path, "/activity") && r.Method == http.MethodDelete:
			cleared++
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"no thread"}}`))
		case strings.HasSuffix(r.URL.Path, "/messages"):
			key, _ := body["client_key"].(string)
			attempts[key]++
			switch {
			case key == "flaky" && attempts[key] == 1:
				w.WriteHeader(502)
				_, _ = w.Write([]byte(`{"error":{"code":"upstream","message":"bad gateway"}}`))
			case key == "busy" && attempts[key] == 1:
				w.Header().Set("Retry-After", "3")
				w.WriteHeader(429)
				_, _ = w.Write([]byte(`{"error":{"code":"rate_limited","message":"slow down"}}`))
			case key == "":
				w.WriteHeader(500)
				_, _ = w.Write([]byte(`{"error":{"code":"boom","message":"no"}}`))
			case key == "bad":
				w.WriteHeader(422)
				_, _ = w.Write([]byte(`{"error":{"code":"invalid","message":"blank body"}}`))
			default:
				status := 201
				if attempts[key] > 1 {
					status = 200
				}
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"message":{"id":"m-` + key + `","body":"hi","origin":"token"},"thread":{"id":"t1"},"created":` + map[bool]string{true: "true", false: "false"}[status == 201] + `}`))
			}
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Token: "fc_t"}
	me, err := c.Me(context.Background())
	if err != nil || !me.Has("activity") || !me.Has("idempotency") || me.Has("push") {
		t.Fatalf("features: %v %v", me.Features, err)
	}
	applied, err := c.SetActivity(context.Background(), "ext:s", Activity{Text: "Running tests", Kind: "tool", TTLSeconds: 90, Seq: 42})
	if err != nil || !applied || len(activity) != 1 || activity[0]["kind"] != "tool" || activity[0]["seq"] != float64(42) || activity[0]["ttl_seconds"] != float64(90) {
		t.Fatalf("activity = %v applied=%v err=%v", activity, applied, err)
	}
	if err := c.ClearActivity(context.Background(), "ext:s"); err != nil || cleared != 1 {
		t.Fatalf("clear: %v (%d)", err, cleared)
	}
	// 502 then success: retried once, one message, origin parsed.
	msg, _, err := c.Post(context.Background(), "ext:s", PostRequest{Body: "hi", ClientKey: "flaky"})
	if err != nil || msg.ID != "m-flaky" || msg.Origin != "token" || attempts["flaky"] != 2 || len(slept) != 1 || slept[0] != time.Second {
		t.Fatalf("flaky: msg=%+v err=%v attempts=%d slept=%v", msg, err, attempts["flaky"], slept)
	}
	// 429 with Retry-After: the wait is the server's.
	slept = nil
	if _, _, err := c.Post(context.Background(), "ext:s", PostRequest{Body: "hi", ClientKey: "busy"}); err != nil || attempts["busy"] != 2 || len(slept) != 1 || slept[0] != 3*time.Second {
		t.Fatalf("busy: err=%v attempts=%d slept=%v", err, attempts["busy"], slept)
	}
	// A 4xx is final even with a key.
	slept = nil
	_, _, err = c.Post(context.Background(), "ext:s", PostRequest{Body: "", ClientKey: "bad"})
	var e *Error
	if !errors.As(err, &e) || e.Status != 422 || e.RequestID != "req-1" || attempts["bad"] != 1 || len(slept) != 0 {
		t.Fatalf("bad: %v attempts=%d slept=%v", err, attempts["bad"], slept)
	}
	// Without a key nothing is retried.
	_, _, err = c.Post(context.Background(), "ext:s", PostRequest{Body: "hi"})
	if !errors.As(err, &e) || e.Status != 500 || attempts[""] != 1 || len(slept) != 0 {
		t.Fatalf("no key: %v attempts=%d slept=%v", err, attempts[""], slept)
	}
}
