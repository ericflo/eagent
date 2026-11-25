package config

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/llm"
)

func TestNormalizeServerURL(t *testing.T) {
	cases := []struct {
		in        string
		wantFirst string
		wantHint  string
		wantErr   bool
	}{
		{"127.0.0.1:8080", "http://127.0.0.1:8080/v1", "", false},
		{"http://localhost:1234", "http://localhost:1234/v1", "", false},
		{"http://localhost:1234/v1/", "http://localhost:1234/v1", "", false},
		{"http://localhost:1234/v1/models", "http://localhost:1234/v1", "", false},
		{"http://localhost:1234/v1/chat/completions", "http://localhost:1234/v1", llm.ProtocolChat, false},
		{"http://localhost:1234/v1/responses", "http://localhost:1234/v1", llm.ProtocolResponses, false},
		{"https://proxy.example/custom/v1/messages", "https://proxy.example/custom/v1", llm.ProtocolAnthropic, false},
		{"https://gpu.example/api", "https://gpu.example/api", "", false},
		{"", "", "", true},
		{"ftp://bad.example", "", "", true},
	}
	for _, tc := range cases {
		cands, hint, err := NormalizeServerURL(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("NormalizeServerURL(%q): expected error", tc.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("NormalizeServerURL(%q): unexpected error: %v", tc.in, err)
			continue
		}
		if len(cands) == 0 || cands[0] != tc.wantFirst || hint != tc.wantHint {
			t.Errorf("NormalizeServerURL(%q) = (%v, %q), want first=%q hint=%q", tc.in, cands, hint, tc.wantFirst, tc.wantHint)
		}
	}
}

func TestProbeServerLMStudioStyle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/models":
			fmt.Fprint(w, `{"object":"list","data":[{"id":"text-embedding-nomic","object":"model"},{"id":"google/gemma-4-12b-qat","object":"model"}]}`)
		case "/api/v0/models":
			fmt.Fprint(w, `{"object":"list","data":[
				{"id":"text-embedding-nomic","type":"embeddings","state":"not-loaded","max_context_length":8192},
				{"id":"google/gemma-4-12b-qat","type":"vlm","arch":"gemma4","quantization":"Q4_0","state":"loaded","max_context_length":262144,"loaded_context_length":262144,"capabilities":["tool_use"]}
			]}`)
		case "/v1/chat/completions", "/v1/responses", "/v1/messages":
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":{"message":"missing required field"}}`)
		default:
			// Mimic LM Studio returning HTTP 200 with an error JSON envelope on unknown routes.
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"error":"Unexpected endpoint or method."}`)
		}
	}))
	defer srv.Close()

	res, err := ProbeServer(context.Background(), ProbeOptions{URL: srv.URL})
	if err != nil {
		t.Fatalf("ProbeServer: %v", err)
	}
	if res.Server.Engine != "lmstudio" {
		t.Fatalf("engine = %q, want lmstudio", res.Server.Engine)
	}
	if res.Server.BaseURL != srv.URL+"/v1" {
		t.Fatalf("base_url = %q", res.Server.BaseURL)
	}
	wantProtos := []string{llm.ProtocolChat, llm.ProtocolResponses, llm.ProtocolAnthropic}
	if !slices.Equal(res.Server.Protocols, wantProtos) {
		t.Fatalf("protocols = %v, want %v", res.Server.Protocols, wantProtos)
	}
	if len(res.Models) != 1 || res.Models[0].ID != "google/gemma-4-12b-qat" {
		t.Fatalf("models = %+v (embedding model should be filtered out)", res.Models)
	}
	m := res.Models[0]
	if m.Context != 262144 || !m.Vision || !slices.Equal(m.Protocols, wantProtos) {
		t.Fatalf("model metadata = %+v", m)
	}
}

func TestProbeServerLMStudioV1ReasoningCapabilities(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/models":
			fmt.Fprint(w, `{"object":"list","data":[{"id":"openai/gpt-oss-20b"},{"id":"google/gemma-4-12b-qat"},{"id":"text-embedding-nomic"}]}`)
		case "/api/v1/models":
			fmt.Fprint(w, `{"models":[
				{
					"type":"llm","key":"openai/gpt-oss-20b","display_name":"GPT-OSS 20B","architecture":"gpt-oss",
					"quantization":{"name":"MXFP4"},"max_context_length":131072,"format":"gguf",
					"loaded_instances":[{"id":"openai/gpt-oss-20b","config":{"context_length":131072}}],
					"capabilities":{"vision":false,"trained_for_tool_use":true,"reasoning":{"allowed_options":["low","medium","high"],"default":"low"}}
				},
				{
					"type":"llm","key":"google/gemma-4-12b-qat","display_name":"Gemma 4 12B QAT","architecture":"gemma4",
					"quantization":{"name":"Q4_0"},"max_context_length":262144,"format":"gguf",
					"loaded_instances":[{"id":"google/gemma-4-12b-qat","config":{"context_length":262144}}],
					"capabilities":{"vision":true,"trained_for_tool_use":true,"reasoning":{"allowed_options":["off","on"],"default":"on"}}
				},
				{
					"type":"embedding","key":"text-embedding-nomic","display_name":"Nomic Embed","max_context_length":2048,"format":"gguf","loaded_instances":[]
				}
			]}`)
		case "/v1/chat/completions", "/v1/responses", "/v1/messages":
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":{"message":"missing required field"}}`)
		default:
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"error":"Unexpected endpoint or method."}`)
		}
	}))
	defer srv.Close()

	res, err := ProbeServer(context.Background(), ProbeOptions{URL: srv.URL})
	if err != nil {
		t.Fatalf("ProbeServer: %v", err)
	}
	if res.Server.Engine != "lmstudio" || len(res.Models) != 2 {
		t.Fatalf("engine=%q models=%+v", res.Server.Engine, res.Models)
	}
	gpt := res.Models[0]
	if gpt.Label != "GPT-OSS 20B" || gpt.Context != 131072 || gpt.Vision || !slices.Equal(gpt.Efforts, []string{"low", "medium", "high"}) {
		t.Fatalf("gpt-oss model = %+v", gpt)
	}
	gemma := res.Models[1]
	if gemma.Label != "Gemma 4 12B QAT" || gemma.Context != 262144 || !gemma.Vision || !slices.Equal(gemma.Efforts, []string{"none", "medium"}) {
		t.Fatalf("gemma model = %+v", gemma)
	}
}

func TestProbeServerLoomStyle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/models":
			fmt.Fprint(w, `{"object":"list","data":[{
				"id":"Inkling-2-Mini",
				"object":"model",
				"owned_by":"local",
				"context_length":262144,
				"reasoning":true,
				"supported_reasoning_levels":["none","minimal","low","medium","high","xhigh","max"],
				"default_reasoning_level":"high",
				"limits":{"max_output_tokens":262144},
				"loom":{"kind":"text","endpoint":"/v1/chat/completions"}
			}]}`)
		case "/v1/chat/completions":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["model"] == "" || body["model"] == nil {
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, `{"error":{"message":"request requires a nonempty model string"}}`)
				return
			}
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":{"message":"missing chat field 'messages'"}}`)
		default:
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["model"] == "" || body["model"] == nil {
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, `{"error":{"message":"request requires a nonempty model string"}}`)
				return
			}
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"error":{"message":"NOT_FOUND"}}`)
		}
	}))
	defer srv.Close()

	res, err := ProbeServer(context.Background(), ProbeOptions{URL: srv.URL})
	if err != nil {
		t.Fatalf("ProbeServer: %v", err)
	}
	if res.Server.Engine != "loom" {
		t.Fatalf("engine = %q, want loom", res.Server.Engine)
	}
	if !slices.Equal(res.Server.Protocols, []string{llm.ProtocolChat}) {
		t.Fatalf("protocols = %v, want [openai-chat]", res.Server.Protocols)
	}
	if len(res.Models) != 1 {
		t.Fatalf("models = %+v", res.Models)
	}
	m := res.Models[0]
	if m.ID != "Inkling-2-Mini" || m.Context != 262144 || m.MaxOutput != 262144 {
		t.Fatalf("model = %+v", m)
	}
	if len(m.Efforts) != 7 || m.Efforts[0] != "none" || m.Efforts[6] != "max" {
		t.Fatalf("efforts = %v", m.Efforts)
	}
}

func TestProbeServerLlamaCppVLLMAnthropicAndAuth(t *testing.T) {
	t.Run("llamacpp", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/v1/models":
				fmt.Fprint(w, `{"object":"list","data":[{"id":"qwen3-32b.gguf","owned_by":"llamacpp","meta":{"n_ctx_train":131072}}]}`)
			case "/props":
				fmt.Fprint(w, `{"total_slots":4,"default_generation_settings":{"n_ctx":32768},"modalities":{"vision":true}}`)
			case "/v1/chat/completions", "/v1/messages":
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, `{"error":"messages required"}`)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer srv.Close()
		res, err := ProbeServer(context.Background(), ProbeOptions{URL: srv.URL})
		if err != nil {
			t.Fatal(err)
		}
		if res.Server.Engine != "llamacpp" || len(res.Models) != 1 || !res.Models[0].Vision || res.Models[0].Context != 131072 {
			t.Fatalf("res = %+v", res)
		}
		if !slices.Equal(res.Server.Protocols, []string{llm.ProtocolChat, llm.ProtocolAnthropic}) {
			t.Fatalf("protocols = %v", res.Server.Protocols)
		}
	})

	t.Run("vllm", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/v1/models":
				fmt.Fprint(w, `{"object":"list","data":[{"id":"Qwen/Qwen3-Coder","owned_by":"vllm","max_model_len":65536}]}`)
			case "/v1/chat/completions", "/v1/responses":
				w.WriteHeader(http.StatusUnprocessableEntity)
				fmt.Fprint(w, `{"detail":"field required"}`)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer srv.Close()
		res, err := ProbeServer(context.Background(), ProbeOptions{URL: srv.URL})
		if err != nil {
			t.Fatal(err)
		}
		if res.Server.Engine != "vllm" || res.Models[0].Context != 65536 {
			t.Fatalf("res = %+v", res)
		}
		if !slices.Equal(res.Server.Protocols, []string{llm.ProtocolChat, llm.ProtocolResponses}) {
			t.Fatalf("protocols = %v", res.Server.Protocols)
		}
	})

	t.Run("anthropic-only without /models", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Path == "/v1/messages" {
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, `{"type":"error","error":{"type":"invalid_request_error","message":"messages: Field required"}}`)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()
		res, err := ProbeServer(context.Background(), ProbeOptions{URL: srv.URL + "/v1/messages"})
		if err != nil {
			t.Fatal(err)
		}
		if res.Server.Protocol != llm.ProtocolAnthropic || !slices.Equal(res.Server.Protocols, []string{llm.ProtocolAnthropic}) {
			t.Fatalf("server = %+v", res.Server)
		}
	})

	t.Run("auth required", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer secret-tok" {
				w.WriteHeader(http.StatusUnauthorized)
				fmt.Fprint(w, `{"error":"unauthorized"}`)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/v1/models":
				fmt.Fprint(w, `{"data":[{"id":"priv-model"}]}`)
			case "/v1/chat/completions":
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, `{"error":"missing messages"}`)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer srv.Close()
		if _, err := ProbeServer(context.Background(), ProbeOptions{URL: srv.URL}); err == nil || !strings.Contains(err.Error(), "authentication") {
			t.Fatalf("expected auth error without key, got %v", err)
		}
		t.Setenv("LOCAL_VLLM_KEY", "secret-tok")
		res, err := ProbeServer(context.Background(), ProbeOptions{URL: srv.URL, KeyEnv: "LOCAL_VLLM_KEY"})
		if err != nil || len(res.Models) != 1 || res.Models[0].ID != "priv-model" {
			t.Fatalf("authed probe = %+v err=%v", res, err)
		}
	})
}

func TestRegisterUpdateRefreshDeleteAndPick(t *testing.T) {
	project := t.TempDir()
	modelName := "local-model-v1"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/models":
			fmt.Fprintf(w, `{"data":[{"id":%q,"context_length":131072}]}`, modelName)
		case "/v1/chat/completions", "/v1/responses":
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":"missing input"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	res, err := RegisterServer(context.Background(), project, ProbeOptions{URL: srv.URL, ID: "my-local", Label: "My Local"}, false)
	if err != nil {
		t.Fatalf("RegisterServer: %v", err)
	}
	if res.Server.ID != "my-local" || res.Server.KeyEnv != "" || len(res.Models) != 1 {
		t.Fatalf("res = %+v", res)
	}
	// Built-in provider IDs cannot be reused.
	if _, err := RegisterServer(context.Background(), project, ProbeOptions{URL: srv.URL, ID: "openai"}, true); err == nil {
		t.Fatal("expected error reusing built-in provider id")
	}
	// Explicit duplicate ID fails.
	if _, err := RegisterServer(context.Background(), project, ProbeOptions{URL: srv.URL, ID: "my-local"}, true); err == nil {
		t.Fatal("expected error on duplicate explicit id")
	}
	// KnownModels puts registered custom server models first.
	known := KnownModels(project)
	if len(known) == 0 || known[0].Provider != "my-local" || known[0].ID != "local-model-v1" {
		t.Fatalf("KnownModels[0] = %+v", known[0])
	}
	// PickRoute resolves keyless custom model and updates context tokens.
	picked, err := PickRoute(project, Defaults().Model, "my-local", "local-model-v1", "medium")
	if err != nil {
		t.Fatalf("PickRoute: %v", err)
	}
	if picked.APIKeyEnv != "" || picked.BaseURL != srv.URL+"/v1" || picked.ContextTokens != 131072 || picked.ReasoningEffort != "medium" {
		t.Fatalf("picked = %+v", picked)
	}
	if ep, err := picked.Endpoint(); err != nil || ep.APIKey != "" {
		t.Fatalf("keyless Endpoint = %+v err=%v", ep, err)
	}
	// An empty EAGENT_API_KEY_ENV overrides the default's TOGETHER_API_KEY.
	t.Setenv("EAGENT_BASE_URL", srv.URL+"/v1")
	t.Setenv("EAGENT_MODEL", "local-model-v1")
	t.Setenv("EAGENT_API_KEY_ENV", "")
	cfg, err := Load(project, "")
	if err != nil {
		t.Fatalf("Load with empty key env: %v", err)
	}
	if cfg.Model.APIKeyEnv != "" {
		t.Fatalf("APIKeyEnv = %q, want empty", cfg.Model.APIKeyEnv)
	}
	// UpdateServer switches default protocol without re-probing.
	upd, err := UpdateServer(context.Background(), project, "my-local", ProbeOptions{Protocol: llm.ProtocolResponses}, true)
	if err != nil || upd.Server.Protocol != llm.ProtocolResponses {
		t.Fatalf("UpdateServer = %+v err=%v", upd, err)
	}
	// RefreshServer picks up newly loaded model.
	modelName = "local-model-v2"
	ref, err := RefreshServer(context.Background(), project, "my-local")
	if err != nil || len(ref.Models) != 1 || ref.Models[0].ID != "local-model-v2" {
		t.Fatalf("RefreshServer = %+v err=%v", ref, err)
	}
	// LiveModels refreshes only custom servers when LocalFetchedAt expires while cloud cache is fresh.
	var probedIDs []string
	oldProbe := SetLiveProbe(func(ctx context.Context, pr Provider) ([]Model, error) {
		probedIDs = append(probedIDs, pr.ID)
		if pr.Custom {
			return []Model{{Provider: pr.ID, ID: "local-model-v3", Label: "local-model-v3", Live: true}}, nil
		}
		return nil, fmt.Errorf("cloud should not be probed")
	})
	defer SetLiveProbe(oldProbe)
	if err := WriteLiveCache(project, &LiveCache{
		FetchedAt:      time.Now(),
		LocalFetchedAt: time.Now().Add(-time.Minute),
		Models:         []Model{{Provider: "together", ID: "cloud-m", Live: true}, {Provider: "my-local", ID: "local-model-v2", Live: true}},
	}); err != nil {
		t.Fatal(err)
	}
	liveRes := LiveModels(context.Background(), project)
	if !liveRes.Refreshed || !slices.Equal(probedIDs, []string{"my-local"}) {
		t.Fatalf("probedIDs=%v refreshed=%v", probedIDs, liveRes.Refreshed)
	}
	// DeleteServer removes from both servers.json and live-models.json.
	if err := DeleteServer(context.Background(), project, "my-local"); err != nil {
		t.Fatal(err)
	}
	if _, ok := ProviderByIDFor(project, "my-local"); ok {
		t.Fatal("deleted server still found")
	}
	for _, m := range KnownModels(project) {
		if m.Provider == "my-local" {
			t.Fatalf("deleted server model lingered: %+v", m)
		}
	}
}
