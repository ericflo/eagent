package runtimecontrol

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
)

func TestRuntimeSwitchToKeylessRegisteredServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/models":
			fmt.Fprint(w, `{"data":[{"id":"local-fast","context_length":65536}]}`)
		case "/v1/chat/completions":
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":"messages required"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	project := t.TempDir()
	_, err := config.RegisterServer(context.Background(), project, config.ProbeOptions{
		URL: srv.URL, ID: "box-local", Label: "Local Box",
	}, false)
	if err != nil {
		t.Fatal(err)
	}

	cfg := config.Defaults()
	cur := FromConfigFor(project, cfg)
	grant, err := Grant(project, "s1")
	if err != nil {
		t.Fatal(err)
	}
	view, err := View(project, "s1", "gen-1", cur, true)
	if err != nil {
		t.Fatal(err)
	}
	// Descriptor includes the custom provider in the orchestrator provider enum.
	foundProv := false
	for _, f := range view.Descriptor.Fields {
		if f.Key == "/routes/orchestrator/provider" {
			foundProv = slices.Contains(f.Shape.Enum, any("box-local"))
		}
	}
	if !foundProv {
		t.Fatal("runtime descriptor missing registered custom server in /routes/orchestrator/provider enum")
	}

	proposal := control.Proposal{
		Operation:       "settings.apply",
		SchemaVersion:   view.Descriptor.SchemaVersion,
		ExpectedVersion: view.Snapshot.Version,
		Generation:      view.Generation,
		Edits: []control.Edit{
			{Op: "set", Key: "/routes/orchestrator/provider", Value: "box-local"},
			{Op: "set", Key: "/routes/orchestrator/model", Value: "local-fast"},
			{Op: "set", Key: "/routes/orchestrator/effort", Value: "low"},
		},
	}
	nextState, err := ApplyValues(project, cur, proposal, view, grant)
	if err != nil {
		t.Fatalf("ApplyValues to keyless local server failed: %v", err)
	}
	got := nextState.Routes["orchestrator"]
	if got.Provider != "box-local" || got.Model != "local-fast" || got.Effort != "low" {
		t.Fatalf("nextState.Routes[orchestrator] = %+v", got)
	}
}
