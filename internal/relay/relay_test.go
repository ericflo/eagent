package relay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
)

func kinds(os []obs) string {
	var k []string
	for _, o := range os {
		k = append(k, o.kind)
	}
	return strings.Join(k, ",")
}

func parseAll(p parser, lines ...string) []obs {
	var out []obs
	for _, l := range lines {
		out = append(out, p.line([]byte(l))...)
	}
	return out
}

func TestCodexParser(t *testing.T) {
	ts := `"timestamp":"2026-10-27T23:56:30.000Z"`
	got := parseAll(codexParser{},
		`{`+ts+`,"type":"session_meta","payload":{"id":"01a1814b","cwd":"/w","timestamp":"2026-10-27T23:56:24Z"}}`,
		`{`+ts+`,"type":"event_msg","payload":{"type":"task_started","turn_id":"t1"}}`,
		`{`+ts+`,"type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","content":[{"type":"text","text":"Today's spike — ✓\nline two"}]}}}`,
		`{`+ts+`,"type":"event_msg","payload":{"type":"item_completed","item":{"type":"AgentMessage","phase":"commentary","content":[{"type":"Text","text":"I'll run the tests."}]}}}`,
		`{`+ts+`,"type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","command":["/bin/bash","-lc","go test ./..."],"parsed_cmd":[{"type":"unknown","cmd":"go test ./..."}]}}}`,
		`{`+ts+`,"type":"event_msg","payload":{"type":"item_completed","item":{"type":"ImageView","path":"file:///w/shot.png"}}}`,
		`{`+ts+`,"type":"event_msg","payload":{"type":"item_completed","item":{"type":"AgentMessage","phase":"final_answer","content":[{"type":"Text","text":"Done."}]}}}`,
		`{`+ts+`,"type":"event_msg","payload":{"type":"token_count","rate_limits":{"primary":{"used_percent":82.5,"window_minutes":10080,"resets_at":1793513508}}}}`,
		`{`+ts+`,"type":"event_msg","payload":{"type":"task_complete","last_agent_message":"Done.","duration_ms":4200}}`,
		`{`+ts+`,"type":"event_msg","payload":{"type":"turn_aborted","reason":"interrupted","duration_ms":10}}`,
		`not json`,
	)
	if k := kinds(got); k != "meta,working,user,progress,activity,image,limit,turn,aborted" {
		t.Fatalf("kinds = %s", k)
	}
	if got[0].id != "01a1814b" || got[2].text != "Today's spike — ✓\nline two" || got[4].text != "running go test ./..." || got[5].text != "/w/shot.png" {
		t.Fatalf("unexpected observations: %+v", got)
	}
	if got[6].used != 82.5 || got[6].window != "weekly" || got[7].text != "Done." || got[7].durationMS != 4200 {
		t.Fatalf("limit/turn wrong: %+v %+v", got[6], got[7])
	}
}

func TestClaudeParser(t *testing.T) {
	p := &claudeParser{}
	ts := `"timestamp":"2026-10-27T23:50:00.000Z"`
	got := parseAll(p,
		`{"type":"user",`+ts+`,"message":{"role":"user","content":"Reply with ONE"}}`,
		`{"type":"assistant",`+ts+`,"message":{"content":[{"type":"thinking","thinking":""}]}}`,
		`{"type":"assistant",`+ts+`,"message":{"content":[{"type":"text","text":"Checking first."},{"type":"tool_use","name":"Bash","input":{"command":"ls -la"}}]}}`,
		`{"type":"user",`+ts+`,"message":{"content":[{"type":"tool_result","content":"x"}]}}`,
		`{"type":"queue-operation","operation":"enqueue",`+ts+`,"content":"Also FOUR"}`,
		`{"type":"assistant",`+ts+`,"message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"/w/a.png"}}]}}`,
		`{"type":"assistant",`+ts+`,"message":{"content":[{"type":"text","text":"ONE"}]}}`,
		`{"type":"assistant","isSidechain":true,`+ts+`,"message":{"content":[{"type":"text","text":"subagent chatter"}]}}`,
		`{"type":"system","subtype":"turn_duration",`+ts+`,"durationMs":5200}`,
		`{"type":"user","isMeta":true,`+ts+`,"message":{"content":"Stop hook feedback:\nNow TWO"}}`,
		`{"type":"user","isMeta":true,`+ts+`,"message":{"content":"<system-reminder>ignored</system-reminder>"}}`,
		`{"type":"assistant",`+ts+`,"message":{"content":[{"type":"text","text":"partial"}]}}`,
		`{"type":"user",`+ts+`,"message":{"content":"[Request interrupted by user]"}}`,
	)
	if k := kinds(got); k != "user,working,progress,activity,user,image,turn,user,working,aborted" {
		t.Fatalf("kinds = %s", k)
	}
	if got[2].text != "Checking first." || got[3].text != "running ls -la" || got[4].text != "Also FOUR" || got[5].text != "/w/a.png" {
		t.Fatalf("unexpected: %+v", got)
	}
	if got[6].text != "ONE" || got[6].durationMS != 5200 || got[7].text != "Now TWO" || got[9].text != "partial" {
		t.Fatalf("turn/hook/abort wrong: %+v", got[6:])
	}
}

// fakeHost records what a seat types.
type fakeHost struct {
	mu     sync.Mutex
	typed  []string
	keys   []string
	screen string
	alive  bool
}

func (h *fakeHost) Name() string                                   { return "fake" }
func (h *fakeHost) Start(string, string, []string, []string) error { return nil }
func (h *fakeHost) Alive(string) bool                              { h.mu.Lock(); defer h.mu.Unlock(); return h.alive }
func (h *fakeHost) PID(string) int                                 { return 1 }
func (h *fakeHost) Paste(_ string, text string) error {
	h.mu.Lock()
	h.typed = append(h.typed, text)
	h.mu.Unlock()
	return nil
}
func (h *fakeHost) Keys(_ string, keys ...string) error {
	h.mu.Lock()
	h.keys = append(h.keys, keys...)
	h.mu.Unlock()
	return nil
}
func (h *fakeHost) Capture(string, int) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.screen, nil
}
func (h *fakeHost) Kill(string) error       { return nil }
func (h *fakeHost) List() ([]Listed, error) { return nil, nil }
func (h *fakeHost) Attach(string) []string  { return nil }

func drain(ch <-chan Event) []Event {
	var out []Event
	for {
		select {
		case ev := <-ch:
			if ev.Kind != KindActivity {
				out = append(out, ev)
			}
		default:
			return out
		}
	}
}

func TestSeatFoldsTurnsAndDeliveries(t *testing.T) {
	h := &fakeHost{alive: true}
	s := newSeat(Spec{Name: "codex", Profile: config.RelayProfile{Agent: "codex"}}, codexAgent{}, h, "eagent-1-codex", "")
	s.state = "idle"
	msg, err := s.Send("r1", "Merge every pending PR.\nThen upgrade.", "queue")
	if err != nil || !strings.Contains(msg, "typed into Codex") {
		t.Fatalf("send: %q %v", msg, err)
	}
	if len(h.typed) != 1 || h.typed[0] != "Merge every pending PR.\nThen upgrade." || h.keys[len(h.keys)-1] != "Enter" {
		t.Fatalf("typed %q keys %q", h.typed, h.keys)
	}
	for _, o := range []obs{
		{kind: "working"},
		{kind: "user", text: "Merge every pending PR.\nThen upgrade."},
		{kind: "progress", text: "Listing PRs."},
		{kind: "activity", text: "running gh pr list", tool: true},
		{kind: "image", text: "/w/shot.png", tool: true},
		{kind: "turn", text: "Merged #42 and #44.", durationMS: 1000},
	} {
		s.fold(o)
	}
	evs := drain(s.events)
	var got []string
	for _, ev := range evs {
		switch ev.Kind {
		case KindState:
			got = append(got, "state:"+ev.State)
		case KindDelivered:
			got = append(got, fmt.Sprintf("delivered:%s:%v", ev.SendID, ev.OK))
		case KindTurn:
			got = append(got, fmt.Sprintf("turn:%s:%d:%v", ev.Text, ev.Tools, ev.Images))
		default:
			got = append(got, ev.Kind+":"+ev.Text)
		}
	}
	want := "state:working|delivered:r1:true|progress:Listing PRs.|turn:Merged #42 and #44.:2:[/w/shot.png]|state:idle"
	if strings.Join(got, "|") != want {
		t.Fatalf("events\n got %s\nwant %s", strings.Join(got, "|"), want)
	}
	if len(s.Snapshot().Pending) != 0 {
		t.Fatalf("pending not cleared: %v", s.Snapshot().Pending)
	}
}

func TestSeatLimitThresholds(t *testing.T) {
	s := newSeat(Spec{}, codexAgent{}, &fakeHost{alive: true}, "x", "")
	for _, u := range []float64{10, 49, 51, 60, 81, 82, 96} {
		s.fold(obs{kind: "limit", used: u, window: "weekly"})
	}
	var got []float64
	for _, ev := range drain(s.events) {
		got = append(got, ev.UsedPercent)
	}
	if fmt.Sprint(got) != "[51 81 96]" {
		t.Fatalf("limit events at %v", got)
	}
}

func TestSeatSkipsHistoryOnAttach(t *testing.T) {
	s := newSeat(Spec{}, codexAgent{}, &fakeHost{alive: true}, "x", "")
	s.state = "idle"
	s.since = time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	s.fold(obs{kind: "turn", text: "old", at: s.since.Add(-time.Minute)})
	s.fold(obs{kind: "turn", text: "new", at: s.since.Add(time.Minute)})
	var texts []string
	for _, ev := range drain(s.events) {
		if ev.Kind == KindTurn {
			texts = append(texts, ev.Text)
		}
	}
	if fmt.Sprint(texts) != "[new]" {
		t.Fatalf("turns after attach: %v", texts)
	}
}

func TestSeatAnswersStartupDialogs(t *testing.T) {
	h := &fakeHost{alive: true, screen: " Quick safety check: Is this a project you created or one you trust?\n ❯ No, exit\n   Yes, I trust this folder\n"}
	s := newSeat(Spec{}, claudeAgent{}, h, "x", "")
	s.checkScreen(time.Now())
	if strings.Join(h.keys, " ") != "Down Enter" {
		t.Fatalf("keys for the trust prompt = %q", h.keys)
	}
	h.screen = "❯ Try \"refactor <filepath>\"\n  ⏵⏵ bypass permissions on (shift+tab to cycle)"
	now := time.Now()
	s.checkScreen(now)
	if s.Snapshot().State != "starting" {
		t.Fatalf("one ready-looking screen is not enough (Codex flashes one before its trust prompt)")
	}
	s.checkScreen(now.Add(1500 * time.Millisecond))
	if s.Snapshot().State != "idle" {
		t.Fatalf("an agent at its prompt should be idle, is %s", s.Snapshot().State)
	}
}

func TestStopHook(t *testing.T) {
	spool := t.TempDir()
	var out bytes.Buffer
	RunStopHook(spool, strings.NewReader("{}"), &out)
	if out.Len() != 0 {
		t.Fatalf("empty spool should let the turn end, got %q", out.String())
	}
	s := &Seat{spec: Spec{Spool: spool}}
	_ = claudeAgent{}.afterTurn(s, "r1", "first")
	time.Sleep(2 * time.Millisecond)
	_ = claudeAgent{}.afterTurn(s, "r2", "second")
	RunStopHook(spool, strings.NewReader("{}"), &out)
	var got map[string]string
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("hook output %q: %v", out.String(), err)
	}
	if got["decision"] != "block" || got["reason"] != "first\n\nsecond" {
		t.Fatalf("hook output = %v", got)
	}
	if left, _ := os.ReadDir(spool); len(left) != 0 {
		t.Fatalf("spool not emptied: %v", left)
	}
}

func TestTailerPartialLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.jsonl")
	os.WriteFile(path, []byte("{\"a\":1}\n{\"b\":"), 0o644)
	tl := &tailer{path: path}
	lines, _ := tl.next()
	if len(lines) != 1 || string(lines[0]) != `{"a":1}` {
		t.Fatalf("first read %q", lines)
	}
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("2}\n")
	f.Close()
	lines, _ = tl.next()
	if len(lines) != 1 || string(lines[0]) != `{"b":2}` {
		t.Fatalf("second read %q", lines)
	}
	mid := &tailer{path: path, offset: 3, skipPartial: true}
	lines, _ = mid.next()
	if len(lines) != 1 || string(lines[0]) != `{"b":2}` {
		t.Fatalf("mid-file read %q", lines)
	}
}

func TestCodexLocate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	work := t.TempDir()
	launched := time.Now()
	day := filepath.Join(home, "sessions", launched.Format("2006"), launched.Format("01"), launched.Format("02"))
	os.MkdirAll(day, 0o755)
	write := func(name, id, cwd string, start time.Time) string {
		p := filepath.Join(day, name)
		// Field order as Codex writes it: identity first, the long base
		// instructions last (long enough here to overflow the read).
		type payload struct {
			ID           string `json:"id"`
			Timestamp    string `json:"timestamp"`
			Cwd          string `json:"cwd"`
			Instructions string `json:"base_instructions"`
		}
		meta, _ := json.Marshal(struct {
			Timestamp string  `json:"timestamp"`
			Type      string  `json:"type"`
			Payload   payload `json:"payload"`
		}{start.Format(time.RFC3339Nano), "session_meta", payload{id, start.Format(time.RFC3339Nano), cwd, strings.Repeat("x", 300000)}})
		os.WriteFile(p, append(meta, '\n'), 0o644)
		return p
	}
	write("rollout-a.jsonl", "old", work, launched.Add(-time.Hour))
	write("rollout-b.jsonl", "elsewhere", t.TempDir(), launched.Add(time.Second))
	want := write("rollout-c.jsonl", "mine", work, launched.Add(2*time.Second))
	path, id := codexAgent{}.locate(work, launched, "")
	if path != want || id != "mine" {
		t.Fatalf("locate = %s %s", path, id)
	}
	if p, _ := (codexAgent{}).locate(work, launched, "old"); p == "" {
		t.Fatalf("resume lookup by id found nothing")
	}
}

func TestCommands(t *testing.T) {
	codex := codexAgent{}.command(config.BuiltinRelayProfiles()["codex"], "", false, "")
	if strings.Join(codex, " ") != `codex --yolo -m gpt-6-astra -c model_reasoning_effort="xhigh"` {
		t.Fatalf("codex argv %q", codex)
	}
	resume := codexAgent{}.command(config.BuiltinRelayProfiles()["codex"], "abc", true, "")
	if strings.Join(resume[:3], " ") != "codex resume abc" {
		t.Fatalf("codex resume argv %q", resume)
	}
	claude := claudeAgent{}.command(config.BuiltinRelayProfiles()["claude"], "u-1", false, "/bin/eagent relay-hook /spool")
	joined := strings.Join(claude, " ")
	if !strings.HasPrefix(joined, "claude --session-id u-1 --model claude-opus-5-5 --effort xhigh --settings ") || !strings.HasSuffix(joined, "--dangerously-skip-permissions") || !strings.Contains(joined, `relay-hook /spool`) {
		t.Fatalf("claude argv %q", claude)
	}
	if id := (claudeAgent{}).chooseID(); len(id) != 36 || id[14] != '4' {
		t.Fatalf("not a v4 uuid: %s", id)
	}
}

func TestTmuxPasteIsExact(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	old := Socket
	Socket = fmt.Sprintf("eagent-test-%d", os.Getpid())
	defer func() {
		KillSocket(Socket)
		Socket = old
	}()
	dir := t.TempDir()
	out := filepath.Join(dir, "out.txt")
	h := Tmux{}
	name := Target("1", "cat")
	if err := h.Start(name, dir, []string{"sh", "-c", "cat > " + out}, nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if !h.Alive(name) {
		t.Fatalf("session not alive")
	}
	text := "Today's mission — “quotes” 日本語 $HOME `cmd`\nline 2\twith tab"
	if err := h.Paste(name, text); err != nil {
		t.Fatal(err)
	}
	h.Keys(name, "Enter")
	time.Sleep(200 * time.Millisecond)
	h.Keys(name, "C-d")
	deadline := time.Now().Add(3 * time.Second)
	for h.Alive(name) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	got, _ := os.ReadFile(out)
	if string(got) != text+"\n" {
		t.Fatalf("pasted %q, want %q", got, text+"\n")
	}
	if h.Alive(name) {
		t.Fatalf("still alive after the program exited")
	}
	listed, _ := h.List()
	if len(listed) != 1 || listed[0].Name != name || !listed[0].Dead {
		t.Fatalf("list = %+v", listed)
	}
}

func TestExpandHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for in, want := range map[string]string{
		"~":                 home,
		"~/Development/foo": filepath.Join(home, "Development/foo"),
		"/srv/app":          "/srv/app",
		"rel/dir":           "rel/dir",
		"~bob/x":            "~bob/x",
		"":                  "",
	} {
		if got := ExpandHome(in); got != want {
			t.Errorf("ExpandHome(%q) = %q, want %q", in, got, want)
		}
	}
}
