// A private, synthetic archive for the browser regression test. Never runs a
// harness, contacts a provider, or reads the user's configuration directory.
package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ericflo/eagent/internal/archive"
	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/store"
)

func main() {
	if len(os.Args) != 2 {
		panic("fixture output directory is required")
	}
	for _, value := range os.Environ() {
		if key, _, _ := strings.Cut(value, "="); strings.HasPrefix(key, "EAGENT_") {
			_ = os.Unsetenv(key)
		}
	}
	_ = os.Setenv("EAGENT_FINALECHAT", "off")
	project := filepath.Join(os.Args[1], "project")
	must(os.MkdirAll(filepath.Dir(config.File(project)), 0700))
	must(os.WriteFile(config.File(project), []byte(`{"preset":"glm","task_concurrency":5,"unknown_secret":"fixture-must-not-be-published"}`), 0600))
	cfg := config.Defaults()
	cfg.TaskConcurrency = 7
	_, err := config.SaveBundle(project, "fixture", "Browser fixture", cfg)
	must(err)
	session, err := store.Create(store.Root(project), time.UnixMilli(1788800000000))
	must(err)
	_, err = session.Append(event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{}))
	must(err)
	_, err = session.Append(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "Synthetic browser fixture"}))
	must(err)
	must(session.Close())
	exported, err := archive.SnapshotIn(context.Background(), project, session.ID, "browser-test", os.Args[1])
	must(err)
	defer exported.Close()
	must(os.Rename(exported.Dir, filepath.Join(os.Args[1], "archive")))
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
