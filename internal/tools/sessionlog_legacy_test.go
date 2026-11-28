package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// Lookups over a log from before eagent was one loop print its task, note,
// yield and process events in words, as main's Summarize did, not as JSON.
func TestSummarizeLegacyEventsReadably(t *testing.T) {
	ev := func(typ, actor, task, data string) event.Event {
		return event.Event{Type: typ, Actor: actor, Task: task, Data: json.RawMessage(data)}
	}
	cases := []struct {
		ev   event.Event
		want string
	}{
		{ev("task.create", "orchestrator", "", `{"id":"t1","title":"Review the code","description":"look for bugs","kind":"work"}`), "t1 Review the code: look for bugs"},
		{ev("task.create", "orchestrator", "", `{"id":"t2","title":"Notes","description":"write them","kind":"dossier"}`), "t2 (notes for a fresh context) Notes: write them"},
		{ev("task.start", "harness", "t1", `{"id":"t1"}`), "t1 started"},
		{ev("task.end", "harness", "t1", `{"id":"t1","status":"completed","summary":"found two bugs","turns":4}`), "t1 completed: found two bugs"},
		{ev("note", "orchestrator", "", `{"text":"the build is green"}`), "the build is green"},
		{ev("note", "orchestrator", "", `{"text":"yes, it is","answer":true}`), "(answer) yes, it is"},
		{ev("yield", "orchestrator", "", `{"done":true,"reason":"all fixed"}`), "done: all fixed"},
		{ev("yield", "orchestrator", "", `{"done":false,"reason":"waiting on the user"}`), "waiting: waiting on the user"},
		{ev("proc.start", "task", "t1", `{"handle":"p1","command":"go test ./...","cwd":"/p","timeout_s":600}`), "p1 go test ./... (in /p)"},
		{ev("proc.pid", "task", "t1", `{"handle":"p1","pid":4242}`), "p1 pid 4242"},
		{ev("proc.exit", "task", "t1", `{"handle":"p1","exit_code":1,"reason":"exited","duration_ms":9}`), "p1 exited code=1"},
		{ev("narrator.trim", "narrator", "", `{"from_seq":12,"recap":"so far"}`), "narrator conversation trimmed from #12: so far"},
	}
	for _, c := range cases {
		got := Summarize(c.ev, 400)
		if !strings.HasSuffix(got, ": "+c.want) || strings.Contains(got, "{") {
			t.Errorf("%s: got %q, want it to end with %q", c.ev.Type, got, c.want)
		}
	}
	if got := Summarize(ev("task.end", "harness", "t1", `{"id":"t1","status":"failed","summary":"x"}`), 0); !strings.Contains(got, " task.end harness(t1): ") {
		t.Errorf("the task id is not kept beside the actor: %q", got)
	}
}
