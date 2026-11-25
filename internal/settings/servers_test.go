package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/llm"
	"github.com/ericflo/eagent/internal/protocol/control"
)

func mockLocalServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/models":
			fmt.Fprint(w, `{"object":"list","data":[{"id":"local-coder","context_length":131072,"reasoning":true,"supported_reasoning_levels":["none","low","medium","high"]}]}`)
		case "/v1/chat/completions":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["messages"] == nil {
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, `{"error":"messages required"}`)
				return
			}
			fmt.Fprint(w, `{"model":"local-coder","choices":[{"message":{"content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"ping","arguments":"{\"message\":\"pong\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":15,"completion_tokens":8}}`)
		case "/v1/responses", "/v1/messages":
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":"input required"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestRemoteSnapshotApplyAndEditorDataWithRegisteredServer(t *testing.T) {
	srv := mockLocalServer(t)
	defer srv.Close()

	project := t.TempDir()
	s := &Service{Project: project}
	g := s.Grant()

	before, err := s.RemoteSnapshot(g)
	if err != nil {
		t.Fatal(err)
	}
	// Register a local server and verify snapshot version invalidates and suggestions include it.
	reg, err := config.RegisterServer(context.Background(), project, config.ProbeOptions{
		URL:   srv.URL,
		ID:    "my-local",
		Label: "My Local Box",
	}, false)
	if err != nil {
		t.Fatal(err)
	}

	view, err := s.RemoteSnapshot(g)
	if err != nil {
		t.Fatal(err)
	}
	if view.Snapshot.Version == before.Snapshot.Version {
		t.Fatal("registering a server did not change RemoteSnapshot version")
	}
	// The model field offers the local server's models first, and its
	// address is a base URL to choose.
	fields := map[string]control.Field{}
	for _, f := range view.Descriptor.Fields {
		fields[f.Key] = f
	}
	if f := fields["/model/model"]; f.Suggest == nil || f.Suggest.List != "models" || !slices.Contains(fields["/model/base_url"].Shape.Enum, any(reg.Server.BaseURL)) {
		t.Fatalf("model fields = %+v %+v", f, fields["/model/base_url"])
	}
	models := view.Descriptor.Suggestions["models"]
	if len(models) == 0 || models[0].Value != "local-coder" {
		t.Fatalf("models[0] = %+v, want local-coder", models)
	}
	// api_key_env field enum allows "" (keyless local server).
	foundEmptyKey := false
	for _, f := range view.Descriptor.Fields {
		if f.Key == "/model/api_key_env" {
			foundEmptyKey = slices.Contains(f.Shape.Enum, any(""))
		}
	}
	if !foundEmptyKey {
		t.Fatal("/model/api_key_env enum does not allow empty string for keyless servers")
	}
	// EditorData for FinaleChat's embedded settings website includes the registered server + model.
	ed, err := s.EditorData()
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Catalog struct {
			Providers []config.Provider `json:"providers"`
			Models    []config.Model    `json:"models"`
		} `json:"catalog"`
	}
	raw, _ := json.Marshal(ed)
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(parsed.Catalog.Providers, func(p config.Provider) bool { return p.ID == "my-local" && p.Custom }) {
		t.Fatalf("EditorData providers missing my-local: %+v", parsed.Catalog.Providers)
	}
	if !slices.ContainsFunc(parsed.Catalog.Models, func(m config.Model) bool { return m.Provider == "my-local" && m.ID == "local-coder" }) {
		t.Fatalf("EditorData models missing local-coder")
	}

	// Applying a remote edit switching the model to the registered keyless server succeeds.
	p := control.Proposal{
		Operation:       "settings.apply",
		SchemaVersion:   SchemaVersion,
		ExpectedVersion: view.Snapshot.Version,
		Edits: []control.Edit{
			{Op: "set", Key: "/model/base_url", Value: reg.Server.BaseURL},
			{Op: "set", Key: "/model/model", Value: "local-coder"},
			{Op: "set", Key: "/model/protocol", Value: llm.ProtocolChat},
			{Op: "set", Key: "/model/api_key_env", Value: ""},
		},
	}
	_, after, err := s.Apply(context.Background(), p, g)
	if err != nil {
		t.Fatalf("Apply keyless local route: %v", err)
	}
	if after.Snapshot.Effective["/model/base_url"] != reg.Server.BaseURL || after.Snapshot.Effective["/model/api_key_env"] != "" {
		t.Fatalf("effective = %v", after.Snapshot.Effective)
	}

	// Pairing a cloud key env (OPENAI_API_KEY) with a keyless custom server must be refused.
	bad := control.Proposal{
		Operation:       "settings.apply",
		SchemaVersion:   SchemaVersion,
		ExpectedVersion: after.Snapshot.Version,
		Edits:           []control.Edit{{Op: "set", Key: "/model/api_key_env", Value: "OPENAI_API_KEY"}},
	}
	if _, _, err := s.Apply(context.Background(), bad, g); err == nil {
		t.Fatal("expected CheckRoutes to refuse pairing OPENAI_API_KEY with keyless custom server")
	}

	// Route test against the registered keyless server works end-to-end.
	tr, err := s.TestRoute(context.Background(), RouteTestRequest{
		Model: config.ModelRoute{
			Protocol:  llm.ProtocolChat,
			BaseURL:   reg.Server.BaseURL,
			Model:     "local-coder",
			APIKeyEnv: "",
		},
	})
	if err != nil {
		t.Fatalf("TestRoute: %v", err)
	}
	if tr.Status != "tool_call" {
		t.Fatalf("TestRoute status = %q err=%q", tr.Status, tr.Error)
	}
}

func TestRemoteApplyRegistersEditsAndRemovesLocalServers(t *testing.T) {
	srv := mockLocalServer(t)
	defer srv.Close()

	project := t.TempDir()
	s := &Service{Project: project}
	g := s.Grant()

	// 1. Add a server via /add_server_url (the native FinaleChat form flow).
	v0, err := s.RemoteSnapshot(g)
	if err != nil {
		t.Fatal(err)
	}
	addProp := control.Proposal{
		Operation:       "settings.apply",
		SchemaVersion:   SchemaVersion,
		ExpectedVersion: v0.Snapshot.Version,
		Edits:           []control.Edit{{Op: "set", Key: "/add_server_url", Value: srv.URL}},
	}
	_, v1, err := s.Apply(context.Background(), addProp, g)
	if err != nil {
		t.Fatalf("Apply /add_server_url: %v", err)
	}
	if v1.Snapshot.Version == v0.Snapshot.Version {
		t.Fatal("adding server via /add_server_url did not advance version")
	}
	servers, err := config.LoadServers(project)
	if err != nil || len(servers) != 1 || servers[0].BaseURL != srv.URL+"/v1" {
		t.Fatalf("servers after /add_server_url = %+v err=%v", servers, err)
	}
	if !slices.ContainsFunc(v1.Descriptor.Suggestions["models"], func(s control.Suggestion) bool { return s.Value == "local-coder" && s.Group == servers[0].BaseURL }) {
		t.Fatalf("suggestions missing newly probed local model: %+v", v1.Descriptor.Suggestions["models"])
	}

	// 2. Edit default protocol via /servers AND switch the model to it in one proposal.
	editProp := control.Proposal{
		Operation:       "settings.apply",
		SchemaVersion:   SchemaVersion,
		ExpectedVersion: v1.Snapshot.Version,
		Edits: []control.Edit{
			{Op: "set", Key: "/servers", Value: []any{map[string]any{"id": servers[0].ID, "url": servers[0].BaseURL, "label": "Renamed Box", "protocol": llm.ProtocolAnthropic}}},
			{Op: "set", Key: "/model/base_url", Value: servers[0].BaseURL},
			{Op: "set", Key: "/model/model", Value: "local-coder"},
			{Op: "set", Key: "/model/protocol", Value: llm.ProtocolAnthropic},
			{Op: "set", Key: "/model/api_key_env", Value: ""},
		},
	}
	_, v2, err := s.Apply(context.Background(), editProp, g)
	if err != nil {
		t.Fatalf("Apply /servers + route: %v", err)
	}
	servers, _ = config.LoadServers(project)
	if len(servers) != 1 || servers[0].Label != "Renamed Box" || servers[0].Protocol != llm.ProtocolAnthropic {
		t.Fatalf("servers after edit = %+v", servers)
	}
	if v2.Snapshot.Effective["/model/protocol"] != llm.ProtocolAnthropic || v2.Snapshot.Effective["/model/api_key_env"] != "" {
		t.Fatalf("effective model = %v", v2.Snapshot.Effective)
	}

	// 3. Remove all registered servers by unsetting /servers.
	rmProp := control.Proposal{
		Operation:       "settings.apply",
		SchemaVersion:   SchemaVersion,
		ExpectedVersion: v2.Snapshot.Version,
		Edits:           []control.Edit{{Op: "unset", Key: "/servers"}},
	}
	if _, _, err := s.Apply(context.Background(), rmProp, g); err != nil {
		t.Fatalf("Apply unset /servers: %v", err)
	}
	if servers, _ = config.LoadServers(project); len(servers) != 0 {
		t.Fatalf("servers after unset = %+v", servers)
	}
}
