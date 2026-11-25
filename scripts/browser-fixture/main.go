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
	must(os.WriteFile(config.File(project), []byte(`{"preset":"glm","max_calls_per_turn":5,"unknown_secret":"fixture-must-not-be-published"}`), 0600))
	cfg := config.Defaults()
	cfg.MaxCallsPerTurn = 7
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
	start := event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{Models: map[string]string{event.ActorEagent: cfg.Model.Model}, Endpoints: map[string]string{event.ActorEagent: cfg.Model.BaseURL}})
	start.Time = time.UnixMilli(1788800000000)
	_, err = session.Append(start)
	must(err)
	message := event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "Synthetic browser fixture"})
	message.Time = start.Time.Add(10 * time.Second)
	message, err = session.Append(message)
	must(err)
	// The agent works and reports after the selected chat message; historical
	// views must never show its report or eagent's tool output before they
	// happened.
	for i, ev := range []event.Event{
		event.New(event.RelayStart, event.ActorHarness, event.RelayStartData{Name: "claude", Profile: "claude", Agent: "claude", AgentLabel: "Claude", Dir: project, Host: "tmux", Target: "fixture-target"}),
		event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: "claude", ID: "r1", From: "user", Seq: message.Seq, Mode: "queue", Text: "the forwarded fixture message", Auto: true}),
		event.New(event.RelayState, event.ActorHarness, event.RelayStateData{Name: "claude", State: "working"}),
		event.New(event.Assistant, event.ActorEagent, event.AssistantData{Model: cfg.Model.Model, Host: cfg.Model.BaseURL, Text: "Looking at the screen", ToolCalls: []event.ToolCall{{ID: "call1", Name: "screen", Args: []byte(`{"relay":"claude"}`)}}}),
		event.New(event.ToolResult, event.ActorEagent, event.ToolResultData{CallID: "call1", Name: "screen", Output: "Archived tool output"}),
		event.New(event.RelayTurn, event.ActorHarness, event.RelayTurnData{Name: "claude", Text: "Archived turn report", DurationMS: 4000, Tools: 2}),
		event.New(event.RelayState, event.ActorHarness, event.RelayStateData{Name: "claude", State: "idle"}),
	} {
		ev.Time = start.Time.Add(time.Duration(15+i*2) * time.Second)
		_, err = session.Append(ev)
		must(err)
	}
	response := event.New(event.Assistant, event.ActorEagent, event.AssistantData{Model: cfg.Model.Model, Host: cfg.Model.BaseURL, Text: "Synthetic response", Usage: event.Usage{Input: 1000000, Output: 100000, Cached: 200000}})
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
