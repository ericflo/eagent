package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/state"
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
	if len(res.Skipped) != 1 || res.Skipped[0].Pointer != "/task/reasoning_effort" || res.Skipped[0].Env != "EAGENT_TASK_REASONING_EFFORT" {
		t.Fatalf("skipped = %v", res.Skipped)
	}
	raw, _ := os.ReadFile(config.File(project))
	if strings.Contains(string(raw), "reasoning_effort") || !strings.Contains(string(raw), `"task_concurrency": 6`) {
		t.Fatalf("file = %s", raw)
	}
	// A value the file already holds for a pinned field survives a save of
	// something else, even though the environment hides it right now.
	if err := os.WriteFile(config.File(project), []byte(`{"task": {"reasoning_effort": "xhigh", "max_tokens": 9000}, "task_concurrency": 6}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg = config.Defaults()
	cfg.Task.MaxTokens = 9000
	cfg.Task.ReasoningEffort = "high" // what the env says; the file says xhigh
	cfg.TaskConcurrency = 7
	w = do(s, "PUT", "/api/config", map[string]any{"config": cfg}, nil)
	if w.Code != 200 {
		t.Fatalf("PUT = %d %s", w.Code, w.Body)
	}
	res = putConfigResult{}
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	raw, _ = os.ReadFile(config.File(project))
	if !strings.Contains(string(raw), `"reasoning_effort": "xhigh"`) || !strings.Contains(string(raw), `"task_concurrency": 7`) || len(res.Kept) != 1 {
		t.Fatalf("pinned value not carried over: %s (kept=%v)", raw, res.Kept)
	}
}

func TestPresetChipsAndPromptsAreClosedSets(t *testing.T) {
	s, project := newTestServer(t)
	// The project file must not leak into a preset's configuration.
	if err := os.MkdirAll(config.BundlesDir(project), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.File(project), []byte(`{"preset": "glm", "task_concurrency": 9}`), 0o644); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "http://127.0.0.1:7331/api/config/presets/astra", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	var pv presetConfigView
	if err := json.Unmarshal(w.Body.Bytes(), &pv); err != nil || w.Code != 200 {
		t.Fatalf("preset = %d %v", w.Code, err)
	}
	if pv.Config.TaskConcurrency == 9 || pv.Config.Preset != "astra" {
		t.Fatalf("preset config carries the project file: %+v", pv.Config.TaskConcurrency)
	}
	// A bundle is shown on its own, over its own preset.
	if err := os.WriteFile(config.BundlePath(project, "mine"), []byte(`{"preset": "anthropic-med", "narrator": {"reasoning_effort": "medium"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	r = httptest.NewRequest("GET", "http://127.0.0.1:7331/api/config/presets/mine", nil)
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	pv = presetConfigView{}
	if err := json.Unmarshal(w.Body.Bytes(), &pv); err != nil || w.Code != 200 || pv.Config.Preset != "anthropic-med" || pv.Config.Orchestrator.Model != "claude-opus-5" || pv.Config.TaskConcurrency == 9 {
		t.Fatalf("bundle = %d %v %+v", w.Code, err, pv.Config.Orchestrator.Model)
	}
	// Anything else is a 404 before any file is opened.
	secret := filepath.Join(project, "secret.json")
	if err := os.WriteFile(secret, []byte(`{"persona": "hidden"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	r = httptest.NewRequest("GET", "http://127.0.0.1:7331/api/config/presets/..%2Fsecret", nil)
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 404 || strings.Contains(w.Body.String(), "hidden") || strings.Contains(w.Body.String(), project) {
		t.Fatalf("traversal = %d %s", w.Code, w.Body)
	}
	// Prompt deletion only ever names one of the known prompts.
	victim := filepath.Join(project, "AGENTS.md")
	if err := os.WriteFile(victim, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	w2 := do(s, "DELETE", "/api/prompts/..%2F..%2FAGENTS", nil, nil)
	if w2.Code != 404 {
		t.Fatalf("delete traversal = %d %s", w2.Code, w2.Body)
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatal("a file outside the prompts directory was deleted")
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

// Tokens spent at one provider stay priced at that provider after a
// fallback switches the actor to another route.
func TestCostIsPricedPerRouteAcrossFallback(t *testing.T) {
	st := state.New()
	seq := int64(0)
	add := func(ev event.Event) {
		seq++
		ev.Seq = seq
		st.Apply(ev)
	}
	add(event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{}))
	add(event.New(event.Route, event.ActorHarness, event.RouteData{Actor: event.ActorOrchestrator, Model: "zai-org/GLM-5.3", BaseURL: "https://api.together.xyz/v1"}))
	add(event.New(event.Assistant, event.ActorOrchestrator, event.AssistantData{Text: "a", Usage: event.Usage{Input: 1_000_000, Cached: 400_000, Output: 20_000}}))
	before, ok := estimateCost(st)
	if !ok || before <= 0 {
		t.Fatalf("before = %v %v", before, ok)
	}
	add(event.New(event.Route, event.ActorHarness, event.RouteData{Actor: event.ActorOrchestrator, Model: "zai-org/GLM-5.3-Flash", BaseURL: "https://api.together.xyz/v1"}))
	add(event.New(event.Assistant, event.ActorOrchestrator, event.AssistantData{Text: "b", Usage: event.Usage{Input: 1000, Output: 10}}))
	after, ok := estimateCost(st)
	if !ok || after < before || after-before > 0.01 {
		t.Fatalf("a fallback repriced the past: before %.6f after %.6f", before, after)
	}
}

// Stopping a session nobody runs is refused instead of leaving a stop in
// its inbox, and a request whose Host is not this server is refused on
// every route by the network wrapper.
func TestStopNeedsARunningSessionAndHostIsChecked(t *testing.T) {
	s, project := newTestServer(t)
	dir := filepath.Join(project, ".agents", "eagent", "sessions", "1788700000001")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ev := `{"seq":1,"ts":"2026-09-07T00:00:00Z","actor":"harness","type":"session.start","data":{"session":"1788700000001","cwd":"` + project + `","interactive":true,"models":{}}}` + "\n" +
		`{"seq":2,"ts":"2026-09-07T00:00:01Z","actor":"harness","type":"session.end","data":{"reason":"done"}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "1788700000001.jsonl"), []byte(ev), 0o644); err != nil {
		t.Fatal(err)
	}
	w := do(s, "POST", "/api/sessions/1788700000001/stop", map[string]any{}, nil)
	if w.Code != 409 {
		t.Fatalf("stop on a finished session = %d %s", w.Code, w.Body)
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, "inbox")); len(entries) != 0 {
		t.Fatalf("a stop was written for a session nobody runs: %v", entries)
	}
	wrapped := s.loopbackOnly(s.Handler())
	for _, c := range []struct {
		host string
		want int
	}{{"evil.example", 403}, {"127.0.0.1:7331", 200}, {"localhost:7331", 200}} {
		r := httptest.NewRequest("GET", "http://x/api/health", nil)
		r.Host = c.host
		r.RemoteAddr = "127.0.0.1:40000"
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, r)
		if rec.Code != c.want {
			t.Errorf("Host %s: %d want %d", c.host, rec.Code, c.want)
		}
	}
	r := httptest.NewRequest("GET", "http://x/api/health", nil)
	r.Host = "127.0.0.1:7331"
	r.RemoteAddr = "10.0.0.5:40000"
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, r)
	if rec.Code != 403 {
		t.Fatalf("a LAN peer got %d", rec.Code)
	}
}
