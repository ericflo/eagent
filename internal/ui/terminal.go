// Package ui renders a session in a terminal: narrator messages on stdout,
// activity and status on stderr, and user input from stdin.
package ui

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/harness"
)

// Terminal is the default UI.
type Terminal struct {
	out         io.Writer // narrator output
	err         *os.File  // status, activity, logs
	tty         bool
	color       bool
	verbose     bool
	jsonOut     bool
	interactive bool
	lastOutput  time.Time
	lastBeat    time.Time
	lastStatus  string

	mu       sync.Mutex
	status   harness.Status
	statusOn bool
	spinner  int
	stream   map[string]*streamState
	idle     bool
	question bool
	input    chan string
	stopSpin chan struct{}
	started  time.Time
}

type streamState struct {
	tool     string
	chars    int
	thinking int
}

// Options configure the terminal.
type Options struct {
	Interactive bool
	Verbose     bool
	JSON        bool
}

// New builds a terminal UI. When interactive, it starts reading stdin.
func New(opts Options) *Terminal {
	t := &Terminal{
		out:         os.Stdout,
		err:         os.Stderr,
		verbose:     opts.Verbose,
		jsonOut:     opts.JSON,
		interactive: opts.Interactive,
		stream:      map[string]*streamState{},
		stopSpin:    make(chan struct{}),
		started:     time.Now(),
		lastOutput:  time.Now(),
	}
	if fi, err := os.Stderr.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
		t.tty = true
		t.color = os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"
	}
	if opts.Interactive {
		t.input = make(chan string)
		go t.readInput()
	}
	if t.tty && !t.interactive {
		go t.spin()
	}
	if t.interactive && !t.jsonOut {
		go t.heartbeat()
	}
	return t
}

// heartbeat prints a status line every 30s while work is happening and
// nothing else has been printed. Interactive sessions cannot use the
// in-place spinner: redrawing the line would erase what the user is typing.
func (t *Terminal) heartbeat() {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			t.mu.Lock()
			busy := t.status.OrchestratorBusy || t.status.NarratorBusy || t.status.Rollover || t.status.TasksRunning+t.status.TasksQueued > 0
			line := t.statusText()
			quiet := time.Since(t.lastOutput) >= 30*time.Second
			changed := line != t.lastStatus || time.Since(t.lastBeat) >= 60*time.Second
			t.mu.Unlock()
			if !busy || line == "" || !quiet || !changed {
				continue
			}
			t.mu.Lock()
			t.lastStatus = line
			t.lastBeat = time.Now()
			t.mu.Unlock()
			t.write(t.err, t.dim("  … "+line)+"\n")
		case <-t.stopSpin:
			return
		}
	}
}

func (t *Terminal) readInput() {
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		t.mu.Lock()
		t.idle = false
		t.question = false
		t.mu.Unlock()
		t.input <- line
	}
	close(t.input)
}

// Input implements harness.UI.
func (t *Terminal) Input() <-chan string { return t.input }

// ---- colours ---------------------------------------------------------------

func (t *Terminal) c(code, s string) string {
	if !t.color {
		return s
	}
	return "\033[" + code + "m" + s + "\033[0m"
}

func (t *Terminal) dim(s string) string  { return t.c("2", s) }
func (t *Terminal) bold(s string) string { return t.c("1", s) }
func (t *Terminal) cyan(s string) string { return t.c("36", s) }
func (t *Terminal) yell(s string) string { return t.c("33", s) }
func (t *Terminal) red(s string) string  { return t.c("31", s) }
func (t *Terminal) grn(s string) string  { return t.c("32", s) }

// ---- status line -------------------------------------------------------------

var frames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func (t *Terminal) spin() {
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			t.mu.Lock()
			t.spinner++
			t.redrawLocked()
			t.mu.Unlock()
		case <-t.stopSpin:
			return
		}
	}
}

// clearLocked erases the status line if it is showing.
func (t *Terminal) clearLocked() {
	if t.statusOn {
		fmt.Fprint(t.err, "\r\033[K")
		t.statusOn = false
	}
}

func (t *Terminal) redrawLocked() {
	if !t.tty || t.interactive || t.idle || t.question {
		t.clearLocked()
		return
	}
	line := t.statusText()
	if line == "" {
		t.clearLocked()
		return
	}
	fmt.Fprintf(t.err, "\r\033[K%s %s", t.cyan(frames[t.spinner%len(frames)]), t.dim(line))
	t.statusOn = true
}

func (t *Terminal) statusText() string {
	s := t.status
	var parts []string
	if s.Rollover {
		parts = append(parts, "writing dossier for a new subsession")
	} else if s.OrchestratorBusy {
		what := "orchestrator thinking"
		if st := t.stream["orchestrator"]; st != nil {
			switch {
			case st.tool != "":
				what = "orchestrator calling " + st.tool
			case st.chars > 0:
				what = "orchestrator writing"
			case st.thinking > 0:
				what = "orchestrator reasoning"
			}
		}
		if s.OrchestratorFor != "" {
			what += " " + s.OrchestratorFor
		}
		parts = append(parts, what)
	}
	if n := s.TasksRunning + s.TasksQueued; n > 0 {
		var names []string
		for k, st := range t.stream {
			if strings.HasPrefix(k, "task:") && st.tool != "" {
				names = append(names, strings.TrimPrefix(k, "task:")+":"+st.tool)
			}
		}
		x := fmt.Sprintf("%d task%s", n, plural(n))
		if s.TasksQueued > 0 {
			x += fmt.Sprintf(" (%d queued)", s.TasksQueued)
		}
		if len(names) > 0 {
			x += " " + strings.Join(names, " ")
		}
		parts = append(parts, x)
	}
	if s.NarratorBusy {
		parts = append(parts, "narrator")
	}
	if s.Procs > 0 {
		parts = append(parts, fmt.Sprintf("%d proc%s", s.Procs, plural(s.Procs)))
	}
	if len(parts) == 0 {
		return "" // nothing is happening; no status line
	}
	if s.ContextTokens > 0 {
		parts = append(parts, fmt.Sprintf("ctx %dk", s.ContextTokens/1000))
	}
	return strings.Join(parts, " · ")
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// Status implements harness.UI.
func (t *Terminal) Status(s harness.Status) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.status = s
	t.redrawLocked()
}

// Stream implements harness.UI.
func (t *Terminal) Stream(actor, task, kind, delta string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := actor
	if task != "" {
		key = "task:" + task
	}
	if kind == "" {
		delete(t.stream, key)
		return
	}
	st := t.stream[key]
	if st == nil {
		st = &streamState{}
		t.stream[key] = st
	}
	switch kind {
	case "tool":
		st.tool = delta
	case "text":
		st.chars += len(delta)
	case "reasoning":
		st.thinking += len(delta)
	}
}

// Idle implements harness.UI: show a prompt when the agent waits for input.
func (t *Terminal) Idle(waiting bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if waiting == t.idle {
		return
	}
	t.idle = waiting
	t.clearLocked()
	if waiting && t.input != nil {
		fmt.Fprint(t.err, t.bold("> "))
	}
}

// ---- output ----------------------------------------------------------------------

// write prints to a stream with the status line cleared and redrawn.
func (t *Terminal) write(w io.Writer, s string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lastOutput = time.Now()
	t.clearLocked()
	if t.idle && t.input != nil && t.tty {
		fmt.Fprint(t.err, "\r\033[K")
	}
	fmt.Fprint(w, s)
	if t.idle && t.input != nil {
		fmt.Fprint(t.err, t.bold("> "))
	} else {
		t.redrawLocked()
	}
}

// Narrate implements harness.UI.
func (t *Terminal) Narrate(text string) {
	if t.jsonOut {
		t.emitJSON(map[string]any{"type": "message", "text": text, "ts": time.Now().UTC().Format(time.RFC3339)})
		return
	}
	var b strings.Builder
	b.WriteString("\n")
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		b.WriteString(t.cyan("│ ") + line + "\n")
	}
	b.WriteString("\n")
	t.write(t.out, b.String())
}

// Ask implements harness.UI.
func (t *Terminal) Ask(id, text string, options []string) {
	if t.jsonOut {
		t.emitJSON(map[string]any{"type": "question", "id": id, "text": text, "options": options, "ts": time.Now().UTC().Format(time.RFC3339)})
		return
	}
	var b strings.Builder
	b.WriteString("\n" + t.yell("? ") + t.bold(text) + "\n")
	for i, o := range options {
		fmt.Fprintf(&b, "  %s %s\n", t.yell(fmt.Sprintf("%d)", i+1)), o)
	}
	t.mu.Lock()
	phone := t.status.Phone
	t.mu.Unlock()
	switch {
	case t.input != nil && phone:
		b.WriteString(t.dim("  (type a number or your answer, or tap it on your phone)") + "\n")
	case t.input != nil:
		b.WriteString(t.dim("  (type a number or your answer)") + "\n")
	case phone:
		b.WriteString(t.dim("  (waiting for your answer on your phone; or: eagent resume --answer \"...\")") + "\n")
	default:
		b.WriteString(t.dim("  (answer with: eagent resume --answer \"...\")") + "\n")
	}
	t.mu.Lock()
	t.question = true
	t.mu.Unlock()
	t.write(t.out, b.String())
	if t.input != nil {
		t.mu.Lock()
		fmt.Fprint(t.err, t.bold("> "))
		t.mu.Unlock()
	}
}

func (t *Terminal) emitJSON(v any) {
	raw, _ := json.Marshal(v)
	t.write(t.out, string(raw)+"\n")
}

// Log implements harness.UI.
func (t *Terminal) Log(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	msg = strings.ReplaceAll(strings.TrimRight(msg, "\n"), "\n", "\n  ")
	t.write(t.err, t.dim("  "+msg)+"\n")
}

// Trace implements harness.UI: activity lines, more with -v.
func (t *Terminal) Trace(ev event.Event) {
	if t.jsonOut && !t.verbose {
		return
	}
	line := t.traceLine(ev)
	if line == "" {
		return
	}
	t.write(t.err, t.dim("  "+line)+"\n")
}

func (t *Terminal) traceLine(ev event.Event) string {
	switch ev.Type {
	case event.TaskCreate:
		var d event.TaskCreateData
		_ = ev.Decode(&d)
		if d.Kind == "dossier" {
			return "▸ " + d.ID + " dossier: summarising the session for a fresh context"
		}
		return "▸ " + d.ID + " delegated: " + d.Title
	case event.TaskEnd:
		var d event.TaskEndData
		_ = ev.Decode(&d)
		mark := t.grn("✓")
		if d.Status != "completed" {
			mark = t.red("✗")
		}
		return fmt.Sprintf("%s %s %s after %d calls", mark, d.ID, d.Status, d.Turns)
	case event.Note:
		var d event.NoteData
		_ = ev.Decode(&d)
		return "✎ " + clip(d.Text, 160)
	case event.Yield:
		var d event.YieldData
		_ = ev.Decode(&d)
		if d.Done {
			return t.grn("■ orchestrator: done. ") + clip(d.Reason, 140)
		}
		return "▫ orchestrator waiting: " + clip(d.Reason, 140)
	case event.SubsessionStart:
		var d event.SubsessionStartData
		_ = ev.Decode(&d)
		if d.Reason == "rollover" {
			return fmt.Sprintf("↻ subsession %d started (%s)", d.Index+1, d.File)
		}
	case event.Dossier:
		return "↻ dossier delivered; orchestrator resuming"
	case event.ScheduleCreate:
		var d event.ScheduleCreateData
		_ = ev.Decode(&d)
		return fmt.Sprintf("⏰ %s %s %q: %s", d.ID, d.Kind, d.Spec, clip(d.Note, 100))
	case event.ScheduleFire:
		var d event.ScheduleFireData
		_ = ev.Decode(&d)
		return "⏰ " + d.ID + " fired"
	case event.Error:
		var d event.ErrorData
		_ = ev.Decode(&d)
		return t.red("! ") + d.Where + ": " + clip(d.Text, 200)
	case event.Route:
		var d event.RouteData
		_ = ev.Decode(&d)
		return "→ " + d.Actor + " now on " + d.Model
	case event.SessionResume:
		var d event.SessionResumeData
		_ = ev.Decode(&d)
		if len(d.Closed) > 0 {
			return "resumed; " + strings.Join(d.Closed, "; ")
		}
		return "resumed"
	}
	if !t.verbose {
		return ""
	}
	who := ev.Actor
	if ev.Task != "" {
		who = ev.Task
	}
	switch ev.Type {
	case event.Assistant:
		var d event.AssistantData
		_ = ev.Decode(&d)
		var parts []string
		for _, tc := range d.ToolCalls {
			parts = append(parts, tc.Name+" "+clip(argPreview(tc), 100))
		}
		s := fmt.Sprintf("%s (%s, %s in/%s out)", who, fmtDur(time.Duration(d.ElapsedMS)*time.Millisecond), k(d.Usage.Input), k(d.Usage.Output))
		if d.Text != "" && ev.Actor != event.ActorNarrator {
			s += ": " + clip(d.Text, 160)
		}
		if len(parts) > 0 {
			s += " → " + strings.Join(parts, "; ")
		}
		return s
	case event.ToolResult:
		var d event.ToolResultData
		_ = ev.Decode(&d)
		if d.Name == "hold" {
			return "narrator holds"
		}
		if d.IsError {
			return fmt.Sprintf("%s %s error: %s", who, d.Name, clip(d.Output, 160))
		}
		return fmt.Sprintf("%s %s: %s", who, d.Name, clip(d.Output, 120))
	case event.ProcStart:
		var d event.ProcStartData
		_ = ev.Decode(&d)
		return fmt.Sprintf("%s $ %s", d.Handle, clip(d.Command, 140))
	case event.ProcExit:
		var d event.ProcExitData
		_ = ev.Decode(&d)
		return fmt.Sprintf("%s %s exit=%d", d.Handle, d.Reason, d.ExitCode)
	case event.TurnStart:
		var d event.TurnData
		_ = ev.Decode(&d)
		return fmt.Sprintf("%s turn (%s)", ev.Actor, d.Reason)
	case event.HarnessMessage:
		var d event.HarnessMessageData
		_ = ev.Decode(&d)
		return "harness→" + who + ": " + clip(d.Text, 140)
	}
	return ""
}

func argPreview(tc event.ToolCall) string {
	var m map[string]any
	if json.Unmarshal(tc.Args, &m) != nil {
		return string(tc.Args)
	}
	for _, key := range []string{"command", "path", "title", "text", "reason", "handle", "query", "spec"} {
		if v, ok := m[key].(string); ok {
			return v
		}
	}
	return string(tc.Args)
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n-1] + "…"
	}
	return s
}

func k(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprint(n)
}

func fmtDur(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return d.Round(100 * time.Millisecond).String()
}

// Banner prints the session header.
func (t *Terminal) Banner(session string, models map[string]string, resumed bool) {
	if t.jsonOut {
		return
	}
	verb := "session"
	if resumed {
		verb = "resumed session"
	}
	t.write(t.err, fmt.Sprintf("%s %s %s\n%s\n",
		t.bold("eagent"), t.dim(verb), t.dim(session),
		t.dim(fmt.Sprintf("  orchestrator %s · task %s · narrator %s", short(models["orchestrator"]), short(models["task"]), short(models["narrator"])))))
}

func short(model string) string {
	if i := strings.LastIndexByte(model, '/'); i >= 0 {
		return model[i+1:]
	}
	return model
}

// End emits the JSON end record (JSON mode only).
func (t *Terminal) End(session, reason string, code int, usage string) {
	if !t.jsonOut {
		return
	}
	t.emitJSON(map[string]any{"type": "end", "session": session, "reason": reason, "exit_code": code, "usage": usage, "ts": time.Now().UTC().Format(time.RFC3339)})
}

// Close stops the spinner and prints a final line.
func (t *Terminal) Close(summary string) {
	select {
	case <-t.stopSpin:
	default:
		close(t.stopSpin)
	}
	t.mu.Lock()
	t.clearLocked()
	t.mu.Unlock()
	if summary != "" && !t.jsonOut {
		t.write(t.err, t.dim(summary)+"\n")
	}
}
