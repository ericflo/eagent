//go:build js && wasm

// The portable viewer runs eagent's actual reducer; derived JSON is a cache.
package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"syscall/js"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/projection"
	"github.com/ericflo/eagent/internal/state"
)

func main() {
	st := state.New()
	var meta projection.Metadata
	lineCounts := map[string]int{}
	register := func(name string, fn func([]js.Value) (any, error)) {
		js.Global().Set(name, js.FuncOf(func(_ js.Value, args []js.Value) any {
			v, err := fn(args)
			out := map[string]any{"result": v}
			if err != nil {
				out = map[string]any{"error": err.Error()}
			}
			raw, _ := json.Marshal(out)
			return string(raw)
		}))
	}
	register("eagentReplayReset", func(args []js.Value) (any, error) {
		st = state.New()
		lineCounts = map[string]int{}
		if len(args) != 1 {
			return nil, fmt.Errorf("metadata required")
		}
		return true, json.Unmarshal([]byte(args[0].String()), &meta)
	})
	register("eagentReplayAppend", func(args []js.Value) (any, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("file and complete lines required")
		}
		file, raw := args[0].String(), args[1].String()
		if !strings.HasSuffix(raw, "\n") {
			return nil, fmt.Errorf("incomplete record")
		}
		for _, line := range strings.Split(strings.TrimSuffix(raw, "\n"), "\n") {
			lineCounts[file]++
			if strings.TrimSpace(line) == "" {
				continue
			}
			var ev event.Event
			if err := json.Unmarshal([]byte(line), &ev); err != nil {
				return nil, err
			}
			if ev.Seq <= st.LastSeq() {
				return nil, fmt.Errorf("event sequence went backwards")
			}
			ev.Source = event.Source{File: file, Line: lineCounts[file]}
			st.Apply(ev)
		}
		return st.LastSeq(), nil
	})
	register("eagentReplayView", func(args []js.Value) (any, error) {
		current := st
		if len(args) > 0 && args[0].Type() == js.TypeNumber && args[0].Int() > 0 {
			until := int64(args[0].Int())
			var evs []event.Event
			for _, ev := range st.Events {
				if ev.Seq > until {
					break
				}
				evs = append(evs, ev)
			}
			current = state.Replay(evs)
		}
		captured := meta
		if len(current.Events) > 0 {
			captured.Modified = current.Events[len(current.Events)-1].Time
		}
		return projection.Detail(captured, current), nil
	})
	register("eagentReplayConversation", func(args []js.Value) (any, error) {
		if len(args) < 2 || len(args) > 3 {
			return nil, fmt.Errorf("actor, task and optional event sequence required")
		}
		current := st
		if len(args) == 3 && args[2].Type() == js.TypeNumber && args[2].Int() > 0 {
			var prefix []event.Event
			for _, ev := range st.Events {
				if ev.Seq > int64(args[2].Int()) {
					break
				}
				prefix = append(prefix, ev)
			}
			current = state.Replay(prefix)
		}
		switch args[0].String() {
		case "orchestrator":
			return current.OrchestratorView(), nil
		case "task":
			return current.TaskView(args[1].String()), nil
		case "narrator":
			return current.NarratorView(nil), nil
		}
		return nil, fmt.Errorf("unknown actor")
	})
	select {}
}
