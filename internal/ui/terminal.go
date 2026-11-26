// Package ui renders a session in a terminal: what eagent says on stdout,
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
	"github.com/ericflo/eagent/internal/relay"
)

// Terminal is the default UI.
type Terminal struct {
	out         io.Writer // what eagent says
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
	stream   streamState
	// who names each agent for people ("Claude"), from relay.start; sends
	// are the user's messages typed into an agent, by send id, until the
	// agent's transcript shows them.
	who      map[string]string
	sends    map[string]string
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
		stopSpin:    make(chan struct{}),
		who:         map[string]string{},
		sends:       map[string]string{},
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
			busy := !t.status.Waiting && (t.status.Busy || t.status.Compacting || len(t.status.Agents) > 0)
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
	switch {
	case s.Compacting:
		parts = append(parts, "eagent writing a briefing for a fresh conversation")
	case s.Busy:
		what := "eagent thinking"
		switch st := t.stream; {
		case st.tool != "":
			what = "eagent calling " + st.tool
		case st.chars > 0:
			what = "eagent writing"
		case st.thinking > 0:
			what = "eagent reasoning"
		}
		if s.BusyFor != "" {
			what += " " + s.BusyFor
		}
		parts = append(parts, what)
	}
	for _, a := range s.Agents {
		who := a.Who
		if who == "" {
			who = a.Name
		}
		parts = append(parts, who+" "+a.State)
	}
	if s.Waiting || len(parts) == 0 {
		return "" // nothing is happening; no status line
	}
	if s.ContextTokens > 0 {
		parts = append(parts, fmt.Sprintf("ctx %dk", s.ContextTokens/1000))
	}
	if s.Phone {
		parts = append(parts, "phone")
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
func (t *Terminal) Stream(kind, delta string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch kind {
	case "":
		t.stream = streamState{}
	case "tool":
		t.stream.tool = delta
	case "text":
		t.stream.chars += len(delta)
	case "reasoning":
		t.stream.thinking += len(delta)
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

// Say implements harness.UI.
func (t *Terminal) Say(text string) {
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

// agentName is how people call one of the session's agents.
func (t *Terminal) agentName(name string) string {
	t.mu.Lock()
	who := t.who[name]
	t.mu.Unlock()
	if who != "" {
		return who
	}
	if a := relay.AgentOf(name); a != "" {
		return relay.AgentLabel(a)
	}
	return name
}

// agentTurn prints the agent's own words at the end of a turn.
func (t *Terminal) agentTurn(d event.RelayTurnData) {
	who := t.agentName(d.Name)
	if t.jsonOut {
		t.emitJSON(map[string]any{"type": "agent_turn", "agent": d.Name, "who": who, "text": d.Text, "duration_ms": d.DurationMS, "tools": d.Tools, "interrupted": d.Interrupted, "ts": time.Now().UTC().Format(time.RFC3339)})
		return
	}
	var meta []string
	if d.DurationMS > 0 {
		meta = append(meta, fmtDur(time.Duration(d.DurationMS)*time.Millisecond))
	}
	if d.Tools > 0 {
		meta = append(meta, fmt.Sprintf("%d tool call%s", d.Tools, plural(d.Tools)))
	}
	if d.Interrupted {
		meta = append(meta, "interrupted")
	}
	head := who + " finished a turn"
	if len(meta) > 0 {
		head += " (" + strings.Join(meta, ", ") + ")"
	}
	var b strings.Builder
	b.WriteString("\n" + t.grn(head) + "\n")
	text := strings.TrimRight(d.Text, "\n")
	if strings.TrimSpace(text) == "" {
		text = t.dim("(it ended the turn without a message)")
	}
	for _, line := range strings.Split(text, "\n") {
		b.WriteString(t.grn("┃ ") + line + "\n")
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
	switch ev.Type {
	case event.RelayStart:
		var d event.RelayStartData
		_ = ev.Decode(&d)
		t.mu.Lock()
		t.who[d.Name] = firstNonEmpty(d.AgentLabel, relay.AgentLabel(d.Agent), d.Name)
		t.mu.Unlock()
	case event.RelayTurn:
		// The agent's report for its turn is the user's to read, whole, as
		// eagent does not restate it.
		var d event.RelayTurnData
		_ = ev.Decode(&d)
		t.agentTurn(d)
		return
	case event.RelaySend:
		var d event.RelaySendData
		_ = ev.Decode(&d)
		if d.From == "user" {
			t.mu.Lock()
			t.sends[d.ID] = d.Name
			t.mu.Unlock()
		}
	case event.RelayDelivered:
		var d event.RelayDeliveredData
		_ = ev.Decode(&d)
		t.mu.Lock()
		name, mine := t.sends[d.ID]
		delete(t.sends, d.ID)
		t.mu.Unlock()
		if !mine || t.jsonOut {
			break
		}
		if d.OK {
			t.write(t.err, t.dim("  ✓ delivered to "+t.agentName(name))+"\n")
		} else if d.Outcome == event.DeliveryNotSent {
			t.write(t.err, t.red("  ✗ ")+t.dim("did not reach "+t.agentName(name)+": "+clip(d.Detail, 160))+"\n")
		} else {
			// Typed (or an older record that does not say): it may have.
			t.write(t.err, t.red("  ✗ ")+t.dim("not confirmed by "+t.agentName(name)+": "+clip(d.Detail, 160))+"\n")
		}
		return
	}
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
	case event.SubsessionStart:
		var d event.SubsessionStartData
		_ = ev.Decode(&d)
		if d.Reason == "rollover" {
			return fmt.Sprintf("↻ subsession %d started (%s)", d.Index+1, d.File)
		}
	case event.Dossier:
		return "↻ briefing written; eagent continues from it"
	case event.ScheduleCreate: // "schedule.create", still written by scheduled wake-ups
		var d struct {
			ID, Kind, Spec, Note string
		}
		_ = ev.Decode(&d)
		return fmt.Sprintf("⏰ %s %s %q: %s", d.ID, d.Kind, d.Spec, clip(d.Note, 100))
	case event.ScheduleFire:
		var d struct{ ID string }
		_ = ev.Decode(&d)
		return "⏰ " + d.ID + " fired"
	case event.ScheduleCancel:
		var d struct{ ID, Reason string }
		_ = ev.Decode(&d)
		s := "⏰ " + d.ID + " cancelled"
		if d.Reason != "" {
			s += ": " + clip(d.Reason, 100)
		}
		return s
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
	case event.RelayStart:
		var d event.RelayStartData
		_ = ev.Decode(&d)
		who := firstNonEmpty(d.AgentLabel, d.Agent, d.Name)
		verb := "started"
		if d.Resumed {
			verb = "resumed"
		}
		return fmt.Sprintf("▸ %s %s in %s", who, verb, d.Dir)
	case event.RelayEnd:
		var d struct {
			Name string `json:"name"`
		}
		_ = ev.Decode(&d)
		return "■ " + d.Name + " stopped"
	}
	if event.IsLegacy(ev) || isLegacyType(ev.Type) {
		if !t.verbose {
			return ""
		}
		return legacyLine(ev)
	}
	if !t.verbose {
		return ""
	}
	who := ev.Actor
	switch ev.Type {
	case event.Assistant:
		var d event.AssistantData
		_ = ev.Decode(&d)
		var parts []string
		for _, tc := range d.ToolCalls {
			parts = append(parts, tc.Name+" "+clip(argPreview(tc), 100))
		}
		s := fmt.Sprintf("%s (%s, %s in/%s out)", who, fmtDur(time.Duration(d.ElapsedMS)*time.Millisecond), k(d.Usage.Input), k(d.Usage.Output))
		if d.Text != "" {
			s += ": " + clip(d.Text, 160)
		}
		if len(parts) > 0 {
			s += " → " + strings.Join(parts, "; ")
		}
		return s
	case event.ToolResult:
		var d event.ToolResultData
		_ = ev.Decode(&d)
		if d.IsError {
			return fmt.Sprintf("%s %s error: %s", who, d.Name, clip(d.Output, 160))
		}
		return fmt.Sprintf("%s %s: %s", who, d.Name, clip(d.Output, 120))
	case event.TurnStart:
		var d event.TurnData
		_ = ev.Decode(&d)
		return fmt.Sprintf("%s turn (%s)", ev.Actor, d.Reason)
	case event.HarnessMessage:
		var d event.HarnessMessageData
		_ = ev.Decode(&d)
		return "harness: " + clip(d.Text, 140)
	case event.RelayState:
		var d event.RelayStateData
		_ = ev.Decode(&d)
		s := d.Name + " " + d.State
		if d.Detail != "" {
			s += ": " + clip(d.Detail, 120)
		}
		return s
	case event.RelaySend:
		var d event.RelaySendData
		_ = ev.Decode(&d)
		return fmt.Sprintf("→ %s (%s, from %s): %s", d.Name, d.Mode, d.From, clip(d.Text, 120))
	}
	return ""
}

// legacyTypes are the event types only old (three-actor) logs hold.
var legacyTypes = map[string]bool{
	event.LegacyTaskCreate: true, event.LegacyTaskStart: true, event.LegacyTaskEnd: true,
	event.LegacyNote: true, event.LegacyYield: true,
	event.LegacyProcStart: true, event.LegacyProcPID: true, event.LegacyProcExit: true,
	event.LegacyNarratorTrim: true,
}

func isLegacyType(typ string) bool { return legacyTypes[typ] }

// legacyLine is a plain line for an event from an old log: who wrote it,
// its type, and its text when it has one.
func legacyLine(ev event.Event) string {
	who := ev.Actor
	if ev.Task != "" {
		who += " " + ev.Task
	}
	var d struct {
		Text   string `json:"text"`
		Title  string `json:"title"`
		Reason string `json:"reason"`
		Output string `json:"output"`
	}
	_ = ev.Decode(&d)
	s := strings.TrimSpace(who + " " + ev.Type)
	if x := firstNonEmpty(d.Text, d.Title, d.Reason, d.Output); x != "" {
		s += ": " + clip(x, 140)
	}
	return s
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}

func argPreview(tc event.ToolCall) string {
	var m map[string]any
	if json.Unmarshal(tc.Args, &m) != nil {
		return string(tc.Args)
	}
	for _, key := range []string{"text", "keys", "query", "reason", "name", "model"} {
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

// Banner prints the session header: the session and eagent's model.
func (t *Terminal) Banner(session, model string, resumed bool) {
	if t.jsonOut {
		return
	}
	verb := "session"
	if resumed {
		verb = "resumed session"
	}
	t.write(t.err, fmt.Sprintf("%s %s %s\n%s\n",
		t.bold("eagent"), t.dim(verb), t.dim(session), t.dim("  model "+short(model))))
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
