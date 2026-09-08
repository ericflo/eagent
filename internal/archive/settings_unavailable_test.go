//go:build unix

package archive

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/store"
)

func TestExportPreservesSessionWithUnavailableSettings(t *testing.T) {
	for _, kind := range []string{"malformed", "oversized", "pipe", "bundle-pipe"} {
		t.Run(kind, func(t *testing.T) {
			project := t.TempDir()
			session, err := store.Create(store.Root(project), time.UnixMilli(1788800000000))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := session.Append(event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{})); err != nil {
				t.Fatal(err)
			}
			if err := session.Close(); err != nil {
				t.Fatal(err)
			}
			logs, err := filepath.Glob(filepath.Join(session.Path, "*.jsonl"))
			if err != nil || len(logs) != 1 {
				t.Fatalf("logs: %v %v", logs, err)
			}
			original, err := os.ReadFile(logs[0])
			if err != nil {
				t.Fatal(err)
			}
			path := config.File(project)
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "malformed":
				err = os.WriteFile(path, []byte(`{"model":"secret-must-not-be-copied",`), 0600)
			case "oversized":
				err = os.WriteFile(path, bytes.Repeat([]byte(" "), (1<<20)+1), 0600)
			case "pipe":
				err = syscall.Mkfifo(path, 0600)
			case "bundle-pipe":
				if err := os.MkdirAll(config.BundlesDir(project), 0700); err != nil {
					t.Fatal(err)
				}
				err = syscall.Mkfifo(config.BundlePath(project, "pipe"), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			captured, err := Snapshot(context.Background(), project, session.ID, "test")
			if err != nil {
				t.Fatal(err)
			}
			defer captured.Close()
			got, err := os.ReadFile(filepath.Join(captured.Dir, "sessions", filepath.Base(logs[0])))
			if err != nil || !bytes.Equal(got, original) {
				t.Fatalf("native bytes: %v", err)
			}
			if kind != "bundle-pipe" {
				if captured.Manifest.SettingsEntrypoint != "" || captured.Manifest.Dataset["settings_availability"] != "unavailable" {
					t.Fatal("unavailable settings advertised")
				}
				if _, ok := captured.Manifest.Find("context/settings-unavailable.json"); !ok {
					t.Fatal("missing availability explanation")
				}
			} else if captured.Manifest.SettingsEntrypoint == "" {
				t.Fatal("one invalid optional bundle disabled the settings page")
			}
			for _, file := range captured.Manifest.Files {
				raw, err := os.ReadFile(filepath.Join(captured.Dir, filepath.FromSlash(file.Path)))
				if err != nil {
					t.Fatal(err)
				}
				if bytes.Contains(raw, []byte("secret-must-not-be-copied")) {
					t.Fatalf("configuration error leaked through %s", file.Path)
				}
			}
		})
	}
}
