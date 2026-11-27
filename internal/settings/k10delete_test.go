package settings

import (
	"context"
	"os"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
)

// The saved preset EAGENT_CONFIG names is the one sessions load, as one the
// file's default_config names is: neither the local editor nor the phone
// deletes it.
func TestK10TheBundleEAGENTCONFIGNamesIsNotDeleted(t *testing.T) {
	clearRouteEnv(t)
	t.Setenv("EAGENT_FINALECHAT", "")
	project := t.TempDir()
	writeProjectFile(t, project, `{"preset":"openai-high"}`)
	if _, err := config.SaveBundle(project, "work", "", config.Defaults()); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EAGENT_CONFIG", "work")
	s := &Service{Project: project}
	if err := s.DeleteBundle(context.Background(), "work"); err == nil {
		t.Fatal("the local editor deleted the saved preset EAGENT_CONFIG names")
	}
	// The phone's path (bundle.delete).
	view, err := s.RemoteSnapshot(s.Grant())
	if err != nil {
		t.Fatal(err)
	}
	del := control.Proposal{Operation: "bundle.delete", SchemaVersion: SchemaVersion, ExpectedVersion: view.Snapshot.Version, Parameters: map[string]any{"name": "work"}}
	editor, err := config.LockEditor(context.Background(), project)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.PrepareResource(editor, del, s.Grant())
	editor.Close()
	if err == nil {
		t.Fatal("the phone may delete the saved preset EAGENT_CONFIG names")
	}
	if _, err := os.Stat(config.BundlePath(project, "work")); err != nil {
		t.Fatalf("the saved preset is gone: %v", err)
	}
}
