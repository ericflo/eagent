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
	// Deliberately differ from the prebuilt viewer's catalog. The WASM replay
	// must use the captured rates to reproduce the Go exporter's estimate.
	for i := range config.Models {
		if p := config.Models[i].Price; p != nil {
			copy := *p
			copy.In *= 2
			copy.Out *= 2
			copy.Cached *= 2
			config.Models[i].Price = &copy
		}
	}
	_, err := config.SaveBundle(project, "fixture", "Browser fixture", cfg)
	must(err)
	session, err := store.Create(store.Root(project), time.UnixMilli(1788800000000))
	must(err)
	start := event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{Models: map[string]string{event.ActorNarrator: cfg.Narrator.Model}, Endpoints: map[string]string{event.ActorNarrator: cfg.Narrator.BaseURL}})
	start.Time = time.UnixMilli(1788800000000)
	_, err = session.Append(start)
	must(err)
	message := event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "Synthetic browser fixture"})
	message.Time = start.Time.Add(10 * time.Second)
	_, err = session.Append(message)
	must(err)
	// A task changes state after the selected chat message; historical views
	// must never show its later report or tool output before they happened.
	for i, ev := range []event.Event{
		event.New(event.TaskCreate, event.ActorOrchestrator, event.TaskCreateData{ID: "t1", Title: "Inspect saved workflow", Description: "Read the archived task details", Kind: "work"}),
		event.New(event.ProcStart, event.ActorTask, event.ProcStartData{Handle: "p1", Command: "echo fixture"}),
		event.New(event.Assistant, event.ActorTask, event.AssistantData{Text: "Reading saved evidence", ToolCalls: []event.ToolCall{{ID: "call1", Name: "shell", Args: []byte(`{"command":"echo fixture"}`)}}}),
		event.New(event.ToolResult, event.ActorTask, event.ToolResultData{CallID: "call1", Name: "shell", Output: "Archived tool output"}),
		event.New(event.TaskEnd, event.ActorTask, event.TaskEndData{ID: "t1", Status: "completed", Summary: "Archived task result", Turns: 1}),
	} {
		ev.Time = start.Time.Add(time.Duration(15+i*2) * time.Second)
		if ev.Actor == event.ActorTask {
			ev.Task = "t1"
		}
		_, err = session.Append(ev)
		must(err)
	}
	response := event.New(event.Assistant, event.ActorNarrator, event.AssistantData{Model: cfg.Narrator.Model, Host: cfg.Narrator.BaseURL, Text: "Synthetic response", Usage: event.Usage{Input: 1000000, Output: 100000, Cached: 200000}})
	response.Time = start.Time.Add(30 * time.Second)
	_, err = session.Append(response)
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
