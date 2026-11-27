package settings

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
)

// EAGENT_BASE_URL moves the route to a 32k server and no layer names a
// window: the window in force is the server's. The user enters a window of
// their own in the editor (16000) and saves. A window the file names holds
// under the variable (TestK1ASaveWritesTheWindowTheFileNamesUnderAMovingAddress),
// so the save should write it and it should apply; at f36e19c it did.
func TestRV15AWindowTheUserEntersUnderAMovingAddressIsWritten(t *testing.T) {
	clearRouteEnv(t)
	project := t.TempDir()
	url := k1LocalGLM(t, project)
	t.Setenv("EAGENT_BASE_URL", url)
	t.Setenv("EAGENT_API_KEY_ENV", "")
	before := config.Resolve(project, "", "")
	t.Logf("before: window %d from %q", before.Effective.Model.ContextTokens, before.Sources["/model/context_tokens"])
	cfg := before.Effective
	cfg.Model.ContextTokens = 16_000
	raw, _ := json.Marshal(cfg)
	loaded, _ := json.Marshal(before.Effective) // as the editor sends it (since baa575e)
	res, err := (&Service{Project: project}).Save(context.Background(), Request{Config: raw, Loaded: loaded})
	if err != nil {
		t.Fatal(err)
	}
	file, _ := os.ReadFile(config.File(project))
	if got := res.Resolution.Effective.Model.ContextTokens; got != 16_000 {
		t.Fatalf("window after save = %d (skipped %+v, kept %+v, file %s), want the 16000 the user entered", got, res.Skipped, res.Kept, file)
	}
}

// The same window from the phone: the field is offered locked as the
// variable's, though a window written to the file would apply.
func TestRV15APhoneCanSetAWindowUnderAMovingAddress(t *testing.T) {
	clearRouteEnv(t)
	project := t.TempDir()
	url := k1LocalGLM(t, project)
	t.Setenv("EAGENT_BASE_URL", url)
	t.Setenv("EAGENT_API_KEY_ENV", "")
	s := &Service{Project: project}
	g := s.Grant()
	view, err := s.RemoteSnapshot(g)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range view.Descriptor.Fields {
		if f.Key == "/model/context_tokens" && !f.Writable {
			t.Fatalf("/model/context_tokens offered locked: %q (source %q)", f.LockedReason, f.Source)
		}
	}
	p := control.Proposal{Operation: "settings.apply", SchemaVersion: SchemaVersion, ExpectedVersion: view.Snapshot.Version, Edits: []control.Edit{{Op: "set", Key: "/model/context_tokens", Value: float64(16000)}}}
	if _, _, err := s.Apply(context.Background(), p, g); err != nil && !strings.Contains(err.Error(), "") {
		t.Fatal(err)
	}
	if got := config.Resolve(project, "", "").Effective.Model.ContextTokens; got != 16_000 {
		t.Fatalf("window after the phone's edit = %d, want 16000", got)
	}
}
