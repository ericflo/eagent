package runtimecontrol

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/artifact"
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
	// Descriptor includes the custom provider in the model's provider enum.
	foundProv := false
	for _, f := range view.Descriptor.Fields {
		if f.Key == "/routes/eagent/provider" {
			foundProv = slices.Contains(f.Shape.Enum, any("box-local"))
		}
	}
	if !foundProv {
		t.Fatal("runtime descriptor missing registered custom server in /routes/eagent/provider enum")
	}

	proposal := control.Proposal{
		Operation:       "settings.apply",
		SchemaVersion:   view.Descriptor.SchemaVersion,
		ExpectedVersion: view.Snapshot.Version,
		Generation:      view.Generation,
		Edits: []control.Edit{
			{Op: "set", Key: "/routes/eagent/provider", Value: "box-local"},
			{Op: "set", Key: "/routes/eagent/model", Value: "local-fast"},
			{Op: "set", Key: "/routes/eagent/effort", Value: "low"},
		},
	}
	nextState, err := ApplyValues(project, cur, proposal, view, grant)
	if err != nil {
		t.Fatalf("ApplyValues to keyless local server failed: %v", err)
	}
	got := nextState.Routes[RouteEagent]
	if got.Provider != "box-local" || got.Model != "local-fast" || got.Effort != "low" {
		t.Fatalf("nextState.Routes[eagent] = %+v", got)
	}
}

func TestRuntimeSavePresetFromRunningSession(t *testing.T) {
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

	var applied Values
	c, err := New(project, "s-save", FromConfigFor(project, config.Defaults()), func(_ Request, v Values, _ map[string]any) error {
		applied = v
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	stop := runLoop(t, c)
	defer stop()

	view, err := View(project, "s-save", c.Generation, c.Values, true)
	if err != nil {
		t.Fatal(err)
	}
	grant, _ := Grant(project, "s-save")

	// Reject colliding with a built-in preset name.
	badProp := control.Proposal{
		Operation:       "settings.apply",
		SchemaVersion:   Schema,
		ExpectedVersion: view.Snapshot.Version,
		Generation:      c.Generation,
		Edits:           []control.Edit{{Op: "set", Key: "/save_preset", Value: "glm"}},
	}
	if _, err := ApplyValues(project, c.Values, badProp, view, grant); err == nil {
		t.Fatal("expected built-in preset name to be rejected in running session")
	}

	// Switch the model to the keyless local server AND save as a new preset in one tap.
	prop := control.Proposal{
		Operation:       "settings.apply",
		SchemaVersion:   Schema,
		ExpectedVersion: view.Snapshot.Version,
		Generation:      c.Generation,
		Edits: []control.Edit{
			{Op: "set", Key: "/routes/eagent/provider", Value: "box-local"},
			{Op: "set", Key: "/routes/eagent/model", Value: "local-fast"},
			{Op: "set", Key: "/routes/eagent/effort", Value: "high"},
			{Op: "set", Key: "/save_preset", Value: "my-session-preset"},
		},
	}
	raw, _ := json.Marshal(prop)
	req := Request{
		ID:       newID(),
		UserID:   "u1",
		Proposal: prop,
		Digest:   artifact.Digest(raw),
		Expires:  time.Now().Add(5 * time.Second),
		Grant:    grant,
	}
	outcome, err := Deliver(context.Background(), project, "s-save", req, false)
	if err != nil || outcome.Status != "succeeded" {
		t.Fatalf("Deliver save_preset: status=%s result=%v err=%v", outcome.Status, outcome.Result, err)
	}
	if applied.Config != "bundle:my-session-preset" {
		t.Fatalf("applied.Config = %q, want bundle:my-session-preset", applied.Config)
	}
	// Project config.json was NOT touched; the new preset was saved in configs/.
	if _, err := os.Stat(config.File(project)); !os.IsNotExist(err) {
		t.Fatal("saving preset from running session wrote project config.json")
	}
	savedCfg, err := config.Load(project, "my-session-preset")
	if err != nil {
		t.Fatalf("Load(my-session-preset): %v", err)
	}
	if savedCfg.Model.Model != "local-fast" || savedCfg.Model.APIKeyEnv != "" || savedCfg.Model.ReasoningEffort != "high" || savedCfg.Model.Fallback != nil || savedCfg.Summarizer.Mode != config.SummarizerInline {
		t.Fatalf("savedCfg = %+v", savedCfg)
	}
	// The next View includes the newly saved preset in /config enum.
	nextView, err := View(project, "s-save", c.Generation, applied, true)
	if err != nil {
		t.Fatal(err)
	}
	at := slices.IndexFunc(nextView.Descriptor.Fields, func(f control.Field) bool { return f.Key == "/config" })
	if at < 0 || !slices.Contains(nextView.Descriptor.Fields[at].Shape.Enum, any("bundle:my-session-preset")) {
		t.Fatalf("/config enum missing newly saved preset: %v", nextView.Descriptor.Fields)
	}
}
