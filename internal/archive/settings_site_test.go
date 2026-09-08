package archive

import (
	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/settings"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSettingsWebsiteDoesNotUploadTranscriptsOrCredentials(t *testing.T) {
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(config.File(project)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.File(project), []byte(`{"task_concurrency":5,"unknown_secret":"must-not-be-published"}`), 0600); err != nil {
		t.Fatal(err)
	}
	service := &settings.Service{Project: project}
	view, err := service.RemoteSnapshot(service.Grant())
	if err != nil {
		t.Fatal(err)
	}
	site, err := SettingsWebsite(project, "1788827736789", "test", view)
	if err != nil {
		t.Fatal(err)
	}
	defer site.Close()
	if site.Manifest.Entrypoint != "settings/index.html" {
		t.Fatal("not a settings website")
	}
	if len(site.Manifest.Files) != 3 {
		t.Fatalf("settings-only website has %d files", len(site.Manifest.Files))
	}
	for _, f := range site.Manifest.Files {
		if strings.HasSuffix(f.Path, ".jsonl") || f.Role == "source" {
			t.Fatal("settings connection uploaded a transcript")
		}
		raw, err := os.ReadFile(filepath.Join(site.Dir, f.Path))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "must-not-be-published") {
			t.Fatal("unknown configuration secret included")
		}
	}
}
