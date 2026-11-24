package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/llm"
)

func TestServersHTTPAPI(t *testing.T) {
	s, _ := newTestServer(t)

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/models":
			fmt.Fprint(w, `{"object":"list","data":[{"id":"demo-local-12b","context_length":131072,"reasoning":true,"supported_reasoning_levels":["none","low","high"]}]}`)
		case "/v1/chat/completions":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["messages"] == nil {
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, `{"error":"messages required"}`)
				return
			}
			fmt.Fprint(w, `{"model":"demo-local-12b","choices":[{"message":{"content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"ping","arguments":"{\"message\":\"pong\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`)
		case "/v1/responses", "/v1/messages":
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":"missing field"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer mock.Close()

	// Guard blocks unauthenticated writes.
	w := do(s, "POST", "/api/servers", map[string]any{"url": mock.URL}, func(r *http.Request) { r.Header.Del("X-Eagent-Token") })
	if w.Code != 403 {
		t.Fatalf("unguarded POST /api/servers = %d, want 403", w.Code)
	}

	// Probe without saving.
	w = do(s, "POST", "/api/servers/probe", map[string]any{"url": mock.URL}, nil)
	if w.Code != 200 {
		t.Fatalf("POST /api/servers/probe = %d %s", w.Code, w.Body)
	}
	var probed config.ProbeResult
	_ = json.Unmarshal(w.Body.Bytes(), &probed)
	if len(probed.Models) != 1 || probed.Models[0].ID != "demo-local-12b" || len(probed.Server.Protocols) != 3 {
		t.Fatalf("probed = %+v", probed)
	}

	// Register server.
	w = do(s, "POST", "/api/servers", map[string]any{"url": mock.URL, "id": "demo-box", "label": "Demo Box"}, nil)
	if w.Code != 200 {
		t.Fatalf("POST /api/servers = %d %s", w.Code, w.Body)
	}

	// GET /api/servers lists it.
	w = do(s, "GET", "/api/servers", nil, nil)
	if w.Code != 200 {
		t.Fatalf("GET /api/servers = %d %s", w.Code, w.Body)
	}
	var listRes struct {
		Servers []config.RegisteredServer `json:"servers"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &listRes)
	if len(listRes.Servers) != 1 || listRes.Servers[0].ID != "demo-box" {
		t.Fatalf("GET /api/servers = %+v", listRes.Servers)
	}

	// GET /api/catalog includes custom server provider + live model.
	w = do(s, "GET", "/api/catalog", nil, nil)
	if w.Code != 200 {
		t.Fatalf("GET /api/catalog = %d %s", w.Code, w.Body)
	}
	var cat struct {
		Providers []config.Provider `json:"providers"`
		Models    []config.Model    `json:"models"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &cat)
	if !slices.ContainsFunc(cat.Providers, func(p config.Provider) bool { return p.ID == "demo-box" && p.Custom }) {
		t.Fatalf("catalog missing demo-box provider")
	}
	if !slices.ContainsFunc(cat.Models, func(m config.Model) bool { return m.Provider == "demo-box" && m.ID == "demo-local-12b" }) {
		t.Fatalf("catalog missing demo-local-12b model")
	}

	// PUT /api/servers/{id} updates default protocol.
	w = do(s, "PUT", "/api/servers/demo-box", map[string]any{"url": mock.URL + "/v1", "protocol": llm.ProtocolAnthropic, "skip_probe": true}, nil)
	if w.Code != 200 {
		t.Fatalf("PUT /api/servers/demo-box = %d %s", w.Code, w.Body)
	}
	var updated config.ProbeResult
	_ = json.Unmarshal(w.Body.Bytes(), &updated)
	if updated.Server.Protocol != llm.ProtocolAnthropic {
		t.Fatalf("updated protocol = %q", updated.Server.Protocol)
	}

	// PUT /api/config saves a keyless route pointing at the registered server.
	cfg := config.Defaults()
	cfg.Orchestrator = config.Actor{Protocol: llm.ProtocolChat, BaseURL: mock.URL + "/v1", Model: "demo-local-12b", APIKeyEnv: "", MaxTokens: 4096, ContextTokens: 131072}
	w = do(s, "PUT", "/api/config", map[string]any{"config": cfg}, nil)
	if w.Code != 200 {
		t.Fatalf("PUT /api/config with local server = %d %s", w.Code, w.Body)
	}

	// POST /api/config/test succeeds against the keyless registered server.
	w = do(s, "POST", "/api/config/test", map[string]any{"actor": cfg.Orchestrator}, nil)
	if w.Code != 200 {
		t.Fatalf("POST /api/config/test = %d %s", w.Code, w.Body)
	}
	var tr struct {
		Status string `json:"status"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &tr)
	if tr.Status != "tool_call" {
		t.Fatalf("test status = %q body=%s", tr.Status, w.Body)
	}

	// POST /api/servers/{id}/probe and /api/servers/refresh work.
	if w = do(s, "POST", "/api/servers/demo-box/probe", map[string]any{}, nil); w.Code != 200 {
		t.Fatalf("POST /api/servers/demo-box/probe = %d %s", w.Code, w.Body)
	}
	if w = do(s, "POST", "/api/servers/refresh", map[string]any{}, nil); w.Code != 200 {
		t.Fatalf("POST /api/servers/refresh = %d %s", w.Code, w.Body)
	}

	// DELETE /api/servers/{id} removes it.
	if w = do(s, "DELETE", "/api/servers/demo-box", nil, nil); w.Code != 200 {
		t.Fatalf("DELETE /api/servers/demo-box = %d %s", w.Code, w.Body)
	}
	if servers, _ := config.LoadServers(s.Project); len(servers) != 0 {
		t.Fatalf("servers after delete = %+v", servers)
	}
}
