package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/config"
)

func newTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	t.Setenv("EAGENT_FINALECHAT", "off")
	t.Setenv("EAGENT_PRESET", "")
	project := t.TempDir()
	return New(project, nil), project
}

// do sends a request the way the page does: JSON, same host, the token.
func do(s *Server, method, path string, body any, mutate func(*http.Request)) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	r := httptest.NewRequest(method, "http://127.0.0.1:7331"+path, &buf)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Eagent-Token", s.token)
	if mutate != nil {
		mutate(r)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func TestGuardRefusesCrossSiteWrites(t *testing.T) {
	s, _ := newTestServer(t)
	cases := []struct {
		name   string
		mutate func(*http.Request)
		want   int
	}{
		{"no token", func(r *http.Request) { r.Header.Del("X-Eagent-Token") }, 403},
		{"wrong token", func(r *http.Request) { r.Header.Set("X-Eagent-Token", "nope") }, 403},
		{"form post", func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }, 415},
		{"foreign host", func(r *http.Request) { r.Host = "evil.example" }, 403},
		{"foreign origin", func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }, 403},
		{"cross-site fetch", func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }, 403},
	}
	for _, c := range cases {
		w := do(s, "PUT", "/api/config", map[string]any{"config": config.Defaults()}, c.mutate)
		if w.Code != c.want {
			t.Errorf("%s: status %d want %d: %s", c.name, w.Code, c.want, w.Body)
		}
	}
	// GET needs none of it.
	r := httptest.NewRequest("GET", "http://127.0.0.1:7331/api/config", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("GET /api/config = %d %s", w.Code, w.Body)
	}
	// The page carries the token.
	r = httptest.NewRequest("GET", "http://127.0.0.1:7331/", nil)
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if !strings.Contains(w.Body.String(), `content="`+s.token+`"`) || strings.Contains(w.Body.String(), "__EAGENT_TOKEN__") {
		t.Fatal("index does not carry the page token")
	}
}

func TestPutConfigOverlayPinAndConflict(t *testing.T) {
	s, project := newTestServer(t)
	cfg := config.Defaults()
	config.Presets["anthropic-med"](&cfg)
	cfg.Task.ReasoningEffort = "medium"
	cfg.TaskConcurrency = 5
	w := do(s, "PUT", "/api/config", map[string]any{"config": cfg, "base_preset": "anthropic-med"}, nil)
	if w.Code != 200 {
		t.Fatalf("PUT = %d %s", w.Code, w.Body)
	}
	var res putConfigResult
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	raw, err := os.ReadFile(config.File(project))
	if err != nil {
		t.Fatal(err)
	}
	var file map[string]json.RawMessage
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if string(file["preset"]) != `"anthropic-med"` || file["orchestrator"] != nil || string(file["task_concurrency"]) != "5" {
		t.Fatalf("overlay file = %s", raw)
	}
	if res.Resolution.Active.Kind != "file" || res.Resolution.Effective.Task.ReasoningEffort != "medium" || res.Resolution.Effective.Orchestrator.Model != "claude-opus-5" {
		t.Fatalf("resolution after save: %+v", res.Resolution.Active)
	}
	// A stale etag is a 409 that carries the current file.
	w = do(s, "PUT", "/api/config", map[string]any{"config": cfg, "base_preset": "anthropic-med", "if_match": "stale"}, nil)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "anthropic-med") {
		t.Fatalf("stale = %d %s", w.Code, w.Body)
	}
	// Pin spells everything out and carries no preset.
	w = do(s, "PUT", "/api/config", map[string]any{"config": cfg, "base_preset": "", "mode": "pin", "if_match": res.Saved.ETag}, nil)
	if w.Code != 200 {
		t.Fatalf("pin = %d %s", w.Code, w.Body)
	}
	raw, _ = os.ReadFile(config.File(project))
	file = nil
	_ = json.Unmarshal(raw, &file)
	if file["preset"] != nil || file["orchestrator"] == nil {
		t.Fatalf("pin file = %s", raw)
	}
	if _, err := os.Stat(config.File(project) + ".bak"); err != nil {
		t.Fatal("no backup written")
	}
}

func TestPutConfigRefusesUnknownHostsKeysAndFields(t *testing.T) {
	s, _ := newTestServer(t)
	cfg := config.Defaults()
	cfg.Task.BaseURL = "https://attacker.example/v1"
	w := do(s, "PUT", "/api/config", map[string]any{"config": cfg}, nil)
	if w.Code != 422 || !strings.Contains(w.Body.String(), "not a catalog provider") {
		t.Fatalf("unknown host = %d %s", w.Code, w.Body)
	}
	cfg = config.Defaults()
	cfg.Narrator.APIKeyEnv = "AWS_SECRET_ACCESS_KEY"
	w = do(s, "PUT", "/api/config", map[string]any{"config": cfg}, nil)
	if w.Code != 422 || !strings.Contains(w.Body.String(), "not a key variable") {
		t.Fatalf("unknown key = %d %s", w.Code, w.Body)
	}
	w = do(s, "PUT", "/api/config", map[string]any{"config": map[string]any{"bogus": 1}}, nil)
	if w.Code != 400 {
		t.Fatalf("unknown field = %d %s", w.Code, w.Body)
	}
	cfg = config.Defaults()
	cfg.NarratorTickSeconds = 1
	w = do(s, "PUT", "/api/config", map[string]any{"config": cfg}, nil)
	if w.Code != 422 || !strings.Contains(w.Body.String(), `"path": "/narrator_tick_seconds"`) && !strings.Contains(w.Body.String(), `"path":"/narrator_tick_seconds"`) {
		t.Fatalf("problem = %d %s", w.Code, w.Body)
	}
	cfg = config.Defaults()
	cfg.Finalechat.BaseURL = "https://attacker.example"
	w = do(s, "PUT", "/api/config", map[string]any{"config": cfg}, nil)
	if w.Code != 422 || !strings.Contains(w.Body.String(), "finalechat") {
		t.Fatalf("phone address = %d %s", w.Code, w.Body)
	}
	// Route tests cannot point a key at an unknown host either.
	a := config.Defaults().Task
	a.BaseURL = "https://attacker.example/v1"
	w = do(s, "POST", "/api/config/test", map[string]any{"actor": a}, nil)
	if w.Code != 422 {
		t.Fatalf("test unknown host = %d %s", w.Code, w.Body)
	}
}

func TestPutConfigSkipsFieldsTheEnvironmentOverrides(t *testing.T) {
	s, project := newTestServer(t)
	t.Setenv("EAGENT_TASK_REASONING_EFFORT", "high")
	cfg := config.Defaults()
	cfg.Task.ReasoningEffort = "medium" // the default is low, so this is a real edit
	cfg.TaskConcurrency = 6
	w := do(s, "PUT", "/api/config", map[string]any{"config": cfg}, nil)
	if w.Code != 200 {
		t.Fatalf("PUT = %d %s", w.Code, w.Body)
	}
	var res putConfigResult
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if len(res.Skipped) != 1 || !strings.Contains(res.Skipped[0], "/task/reasoning_effort") {
		t.Fatalf("skipped = %v", res.Skipped)
	}
	raw, _ := os.ReadFile(config.File(project))
	if strings.Contains(string(raw), "reasoning_effort") || !strings.Contains(string(raw), `"task_concurrency": 6`) {
		t.Fatalf("file = %s", raw)
	}
}

func TestRawRepairAndCatalog(t *testing.T) {
	s, project := newTestServer(t)
	if err := os.MkdirAll(config.BundlesDir(project), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.File(project), []byte(`{"preset": "glm",`), 0o644); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "http://127.0.0.1:7331/api/config", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	var v configView
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil || w.Code != 200 {
		t.Fatalf("GET = %d %v %s", w.Code, err, w.Body)
	}
	if v.Resolution.LoadError == "" || v.Resolution.File.Raw == "" {
		t.Fatalf("broken file not reported: %+v", v.Resolution.File)
	}
	w2 := do(s, "PUT", "/api/config/raw", map[string]any{"raw": `{"preset": "glm", "nope": `}, nil)
	if w2.Code != 422 {
		t.Fatalf("bad raw accepted: %d", w2.Code)
	}
	w2 = do(s, "PUT", "/api/config/raw", map[string]any{"raw": "{\n  \"preset\": \"glm\",\n  \"_note\": \"hand\"\n}\n"}, nil)
	if w2.Code != 200 {
		t.Fatalf("raw = %d %s", w2.Code, w2.Body)
	}
	raw, _ := os.ReadFile(config.File(project))
	if !strings.Contains(string(raw), "hand") {
		t.Fatalf("raw not written: %s", raw)
	}
	r = httptest.NewRequest("GET", "http://127.0.0.1:7331/api/catalog", nil)
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	var cat catalogView
	if err := json.Unmarshal(w.Body.Bytes(), &cat); err != nil || len(cat.Models) == 0 || len(cat.EffortOrder) == 0 {
		t.Fatalf("catalog = %d %v", w.Code, err)
	}
	for k := range cat.Keys {
		if !config.KnownKeyEnv(k) {
			t.Errorf("catalog reveals key presence for %s, which the catalog does not know", k)
		}
	}
}
