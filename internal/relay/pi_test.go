package relay

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
)

// Lines shaped like pi 1.6.0's session files (synthetic text).
const (
	piHeader       = `{"type":"session","version":3,"id":"7f1c2d3e-0000-4000-8000-000000000001","timestamp":"2026-11-24T10:00:00.000Z","cwd":"/work/project"}`
	piModelChange  = `{"type":"model_change","id":"a0000001","parentId":null,"timestamp":"2026-11-24T10:00:00.100Z","provider":"anthropic","modelId":"claude-haiku-5-5"}`
	piSystem       = `{"type":"message","id":"a0000002","parentId":"a0000001","timestamp":"2026-11-24T10:00:01.000Z","message":{"role":"system","content":"","sections":{}}}`
	piUser         = `{"type":"message","id":"a0000003","parentId":"a0000002","timestamp":"2026-11-24T10:00:01.000Z","message":{"role":"user","content":[{"type":"text","text":"Fix the failing test."}],"timestamp":1795000801000}}`
	piToolUse      = `{"type":"message","id":"a0000004","parentId":"a0000003","timestamp":"2026-11-24T10:00:03.000Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"...","thinkingSignature":"x"},{"type":"text","text":"Looking at the test first."},{"type":"toolCall","id":"t1","name":"bash","arguments":{"command":"go test ./...","timeout":60}},{"type":"toolCall","id":"t2","name":"read","arguments":{"path":"/work/project/shot.png"}},{"type":"toolCall","id":"t3","name":"edit","arguments":{"path":"/work/project/main.go","edits":[]}},{"type":"toolCall","id":"t4","name":"web_search","arguments":{"queries":["go test flags"],"numResults":5}},{"type":"toolCall","id":"t5","name":"goal_complete","arguments":{}}],"api":"anthropic-messages","provider":"anthropic","model":"claude-haiku-5-5","usage":{"input":10,"output":5,"totalTokens":15},"stopReason":"toolUse","durationMs":900}}`
	piToolResult   = `{"type":"message","id":"a0000005","parentId":"a0000004","timestamp":"2026-11-24T10:00:04.000Z","message":{"role":"toolResult","toolCallId":"t1","toolName":"bash","content":[{"type":"text","text":"ok"}],"isError":false,"durationMs":100}}`
	piStopText     = `{"type":"message","id":"a0000006","parentId":"a0000005","timestamp":"2026-11-24T10:00:06.000Z","message":{"role":"assistant","content":[{"type":"text","text":"Halfway there."},{"type":"text","text":"Fixed: the test passes now."}],"stopReason":"stop","durationMs":800}}`
	piSettled      = `{"type":"custom","id":"a0000007","parentId":"a0000006","timestamp":"2026-11-24T10:00:06.100Z","customType":"eagent-settled","data":{"aborted":false}}`
	piAbortedMsg   = `{"type":"message","id":"a0000008","parentId":"a0000007","timestamp":"2026-11-24T10:01:00.000Z","message":{"role":"assistant","content":[],"stopReason":"aborted","errorMessage":"Operation aborted","durationMs":10}}`
	piSettledAbort = `{"type":"custom","id":"a0000009","parentId":"a0000008","timestamp":"2026-11-24T10:01:00.100Z","customType":"eagent-settled","data":{"aborted":true}}`
	piErrorMsg     = `{"type":"message","id":"a000000a","parentId":"a0000003","timestamp":"2026-11-24T10:02:00.000Z","message":{"role":"assistant","content":[],"stopReason":"error","errorMessage":"You have reached your usage limit; it resets at 5pm.","durationMs":10}}`
	piContextEdit  = `{"type":"context_edit","id":"a000000b","parentId":"a000000a","timestamp":"2026-11-24T10:02:00.100Z","targetId":"a000000a","replacement":null}`
	piCompaction   = `{"type":"compaction","id":"a000000c","parentId":"a000000b","timestamp":"2026-11-24T10:03:00.000Z","summary":"...","firstKeptEntryId":"a0000003","tokensBefore":1000}`
	piOtherCustom  = `{"type":"custom","id":"a000000d","parentId":"a000000c","timestamp":"2026-11-24T10:03:01.000Z","customType":"web-search-results","data":{}}`
	piPrompt       = `{"type":"custom","id":"a000000e","parentId":"a000000d","timestamp":"2026-11-24T10:03:02.000Z","customType":"eagent-prompt","data":{"kind":"confirm","title":"Replace the goal?"}}`
)

func feed(p parser, lines ...string) []obs { return parseAll(p, lines...) }

func piKinds(list []obs) string { return strings.ReplaceAll(kinds(list), ",", " ") }

func TestPiParserWithExtension(t *testing.T) {
	status := filepath.Join(t.TempDir(), "pi-status.json")
	if err := os.WriteFile(status, []byte(`{"state":"idle"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	p := piAgent{}.newParser(Spec{StatusFile: status})
	got := feed(p, piHeader, piModelChange, piSystem, piUser, piToolUse, piToolResult)
	if piKinds(got) != "meta user working progress activity image activity activity activity" {
		t.Fatalf("kinds %q: %+v", piKinds(got), got)
	}
	if got[0].id != "7f1c2d3e-0000-4000-8000-000000000001" || got[1].text != "Fix the failing test." || got[3].text != "Looking at the test first." {
		t.Fatalf("%+v", got)
	}
	var acts []string
	for _, o := range got[4:] {
		acts = append(acts, o.text)
		if !o.tool {
			t.Fatalf("not counted as a tool: %+v", o)
		}
	}
	if strings.Join(acts, "|") != "running go test ./...|/work/project/shot.png|editing main.go|looking up go test flags|using goal_complete" {
		t.Fatalf("activities %q", acts)
	}
	// A final-looking answer is not the end: only the extension's mark is.
	got = feed(p, piStopText)
	if piKinds(got) != "progress" || got[0].text != "Halfway there." {
		t.Fatalf("stop before the mark: %+v", got)
	}
	got = feed(p, piSettled)
	if piKinds(got) != "turn" || got[0].text != "Fixed: the test passes now." {
		t.Fatalf("settled: %+v", got)
	}
	// An interrupt.
	got = feed(p, piUser, piAbortedMsg, piSettledAbort)
	if piKinds(got) != "user working aborted" {
		t.Fatalf("aborted: %+v", got)
	}
	// A failure pi retries is not the end; one it gives up on is, with the
	// error as the report.
	got = feed(p, piUser, piErrorMsg, piContextEdit, piStopText, piSettled)
	if piKinds(got) != "user working progress turn" || got[3].text != "Fixed: the test passes now." {
		t.Fatalf("retried: %+v", got)
	}
	got = feed(p, piUser, piErrorMsg, piSettledAbort)
	if piKinds(got) != "user working limit aborted" || !got[2].exhausted || !strings.Contains(got[3].text, "usage limit") {
		t.Fatalf("gave up: %+v", got)
	}
	// A dialog is no progress (it would make pi look busy): the seat reads
	// it from the status file and holds the relay blocked.
	got = feed(p, piCompaction, piOtherCustom, piPrompt)
	if piKinds(got) != "compact" {
		t.Fatalf("compaction and prompt: %+v", got)
	}
}

func TestPiParserWithoutExtension(t *testing.T) {
	p := piAgent{}.newParser(Spec{StatusFile: filepath.Join(t.TempDir(), "missing.json")})
	got := feed(p, piHeader, piUser, piStopText)
	if piKinds(got) != "meta user working progress turn" || got[4].text != "Fixed: the test passes now." {
		t.Fatalf("stop: %+v", got)
	}
	got = feed(p, piUser, piAbortedMsg)
	if piKinds(got) != "user working aborted" {
		t.Fatalf("aborted: %+v", got)
	}
	got = feed(p, piUser, piErrorMsg)
	if piKinds(got) != "user working aborted limit" {
		t.Fatalf("error: %+v", got)
	}
	// A mark seen later (a reattach that skipped the start) switches to it.
	got = feed(p, piUser, piSettled)
	if piKinds(got) != "user working turn" {
		t.Fatalf("mark: %+v", got)
	}
	if got = feed(p, piUser, piStopText); piKinds(got) != "user working progress" {
		t.Fatalf("after the mark: %+v", got)
	}
}

func TestPiLocate(t *testing.T) {
	agentDir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "")
	work := filepath.Join(t.TempDir(), "my project")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	a := piAgent{}
	spec := Spec{Dir: work, StatusFile: filepath.Join(t.TempDir(), "pi-status.json")}
	id := "11111111-2222-4333-8444-555555555555"
	// Nothing until pi writes its first message.
	if path, _ := a.locate(spec, time.Now(), id); path != "" {
		t.Fatalf("found %s before it existed", path)
	}
	dir := filepath.Join(agentDir, "sessions", "--"+strings.ReplaceAll(strings.TrimPrefix(work, "/"), "/", "-")+"--")
	if dir != piSessionDir(work) {
		t.Fatalf("session dir %s, want %s", piSessionDir(work), dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "2026-11-24T10-00-00-000Z_"+id+".jsonl")
	if err := os.WriteFile(file, []byte(piHeader+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if path, got := a.locate(spec, time.Now(), id); path != file || got != id {
		t.Fatalf("locate = %s %s", path, got)
	}
	if path, _ := a.locate(spec, time.Now(), "other"); path != "" {
		t.Fatalf("found another id: %s", path)
	}
	// The extension's status names the session pi writes now (after /new).
	other := filepath.Join(dir, "2026-11-24T11-00-00-000Z_new-id.jsonl")
	raw, _ := json.Marshal(piStatus{State: "idle", SessionFile: other, SessionID: "new-id"})
	if err := os.WriteFile(spec.StatusFile, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if path, _ := a.locate(spec, time.Now(), id); path != "" {
		t.Fatalf("followed the old session: %s", path)
	}
	if err := os.WriteFile(other, []byte(piHeader+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if path, got := a.locate(spec, time.Now(), id); path != other || got != "new-id" {
		t.Fatalf("locate after /new = %s %s", path, got)
	}
	// An explicit session directory holds the files directly.
	flat := t.TempDir()
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", flat)
	flatFile := filepath.Join(flat, "2026-11-24T10-00-00-000Z_"+id+".jsonl")
	if err := os.WriteFile(flatFile, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if path, _ := a.locate(Spec{Dir: work}, time.Now(), id); path != flatFile {
		t.Fatalf("locate in session dir = %s", path)
	}
}

// The seat follows pi to the session its status file names.
func TestPiPollFollowsANewSession(t *testing.T) {
	status := filepath.Join(t.TempDir(), "pi-status.json")
	s := newSeat(Spec{StatusFile: status}, piAgent{}, &fakeHost{alive: true}, "x", "first")
	a := piAgent{}
	a.poll(s, time.Now())
	if s.refollow {
		t.Fatal("followed without a status file")
	}
	raw, _ := json.Marshal(piStatus{SessionFile: "/x/first.jsonl", SessionID: "first"})
	_ = os.WriteFile(status, raw, 0o644)
	a.poll(s, time.Now())
	if s.refollow {
		t.Fatal("followed the session it already follows")
	}
	raw, _ = json.Marshal(piStatus{SessionFile: "/x/second.jsonl", SessionID: "second"})
	_ = os.WriteFile(status, raw, 0o644)
	a.poll(s, time.Now())
	if !s.refollow || s.innerID() != "second" || s.since.IsZero() {
		t.Fatalf("did not follow: refollow=%v inner=%s", s.refollow, s.innerID())
	}
}

func TestPiCommandAndPrepare(t *testing.T) {
	a := AgentByName("pi")
	if a == nil {
		t.Fatal("pi is not registered")
	}
	spec := Spec{Profile: config.BuiltinRelayProfiles()["pi"]}
	if err := Prepare(&spec, "/state/relays/pi"); err != nil {
		t.Fatal(err)
	}
	if spec.Hook != "/state/relays/pi/eagent-relay.ts" || spec.Files[spec.Hook] != piExtension || spec.StatusFile == "" || spec.Spool == "" ||
		!slices.Contains(spec.Env, "PI_SKIP_VERSION_CHECK=1") || !slices.Contains(spec.Env, "EAGENT_PI_STATUS="+spec.StatusFile) || !slices.Contains(spec.Env, "EAGENT_PI_SPOOL="+spec.Spool) {
		t.Fatalf("prepared %+v", spec)
	}
	argv := a.command(spec.Profile, "u-1", false, spec.Hook)
	if strings.Join(argv, " ") != "pi --session-id u-1 --approve -e /state/relays/pi/eagent-relay.ts" {
		t.Fatalf("argv %q", argv)
	}
	resumed := a.command(spec.Profile.With("anthropic/claude-haiku-5-5", "low"), "u-1", true, spec.Hook)
	if strings.Join(resumed, " ") != "pi --session-id u-1 --model anthropic/claude-haiku-5-5 --thinking low --approve -e /state/relays/pi/eagent-relay.ts" {
		t.Fatalf("resume argv %q", resumed)
	}
	if id := a.chooseID(); len(id) != 36 {
		t.Fatalf("session id %q", id)
	}
	if keys := a.interruptKeys(); !slices.Equal(keys, []string{"Escape"}) {
		t.Fatalf("interrupt keys %q", keys)
	}
	if argv, err := UpgradeCommand("pi"); err != nil || strings.Join(argv, " ") != "pi update --all" {
		t.Fatalf("upgrade %q %v", argv, err)
	}
	if err := CheckPick(spec.Profile, "openai/gpt-x", "minimal"); err != nil {
		t.Fatal(err)
	}
	if err := CheckPick(spec.Profile, "", "ultra"); err == nil || !strings.Contains(err.Error(), "Pi takes reasoning effort off, minimal") {
		t.Fatalf("effort check: %v", err)
	}
	if !looksReady("x\n0.4%/1.0M (auto)            (anthropic) claude-haiku-5-5 • low\n") || looksReady("Working...") {
		t.Fatal("pi footer not recognised")
	}
	if !containsAll(" Trust project folder?\n → Trust\n   Trust (this session only)\n", piAgent{}.dialogs()[0].match) {
		t.Fatal("trust dialog not recognised")
	}
}

func TestPiAfterTurnSpools(t *testing.T) {
	spool := filepath.Join(t.TempDir(), "after_turn")
	s := newSeat(Spec{Spool: spool}, piAgent{}, &fakeHost{alive: true}, "x", "")
	if err := (piAgent{}).afterTurn(s, "r1", "next thing"); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(spool)
	if len(entries) != 1 || !strings.HasSuffix(entries[0].Name(), "-r1.txt") {
		t.Fatalf("spool %v", entries)
	}
	if (piAgent{}).afterTurn(newSeat(Spec{}, piAgent{}, &fakeHost{alive: true}, "x", ""), "r2", "x") == nil {
		t.Fatal("spooled without a spool")
	}
}

func TestPiChoices(t *testing.T) {
	agentDir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"defaultProvider":"local","defaultModel":"qwen/q-27b","defaultThinkingLevel":"high"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if model, effort := AgentDefaults("pi"); model != "local/qwen/q-27b" || effort != "high" {
		t.Fatal(model, effort)
	}
	if m, e := ProfileDefaults("pi", "", "low"); m != "local/qwen/q-27b" || e != "low" {
		t.Fatal(m, e)
	}
	if got := AgentEfforts("pi"); strings.Join(got, ",") != "off,minimal,low,medium,high,xhigh,max" {
		t.Fatal(got)
	}
	table := "provider     model            context  max-out  thinking  images\n" +
		"big          a                128K     8K       no        no\n" +
		"big          b                128K     8K       yes       no\n" +
		"big          c                128K     8K       no        no\n" +
		"local        qwen/q-27b       200K     32K      yes       no\n" +
		"small        s1               1M       64K      yes       yes\n"
	got := parsePiModels(table, "local")
	var ids []string
	for _, m := range got {
		ids = append(ids, m.ID)
	}
	if strings.Join(ids, ",") != "local/qwen/q-27b,small/s1,big/a,big/b,big/c" || got[0].Detail != "200K context, 32K output, thinking" {
		t.Fatalf("models %v %+v", ids, got[0])
	}
	if ModelListName("pi") != "pi_models" || ModelListName("codex") != "codex_models" {
		t.Fatal("list names")
	}
}

// Every registered agent is complete, and the built-in profiles name
// registered agents.
func TestRegistry(t *testing.T) {
	seen := map[string]bool{}
	for _, a := range Agents() {
		if a.Name() == "" || a.Label() == "" || a.DisplayName() == "" || a.Binary() == "" || seen[a.Name()] {
			t.Fatalf("incomplete or duplicate agent %q", a.Name())
		}
		seen[a.Name()] = true
		if AgentByName(a.Name()) != a {
			t.Fatalf("lookup of %s", a.Name())
		}
		if len(a.interruptKeys()) == 0 || len(a.efforts()) == 0 {
			t.Fatalf("%s has no interrupt keys or efforts", a.Name())
		}
		if argv, err := UpgradeCommand(a.Name()); err != nil || len(argv) < 2 {
			t.Fatalf("%s upgrade %v %v", a.Name(), argv, err)
		}
	}
	if strings.Join(AgentNames(), ",") != "antigravity,claude,codex,pi" {
		t.Fatalf("agents %v", AgentNames())
	}
	builtin := config.BuiltinRelayProfiles()
	for name, p := range builtin {
		a := AgentByName(p.Agent)
		if a == nil || name != p.Agent {
			t.Fatalf("built-in profile %s names agent %q", name, p.Agent)
		}
		if argv := a.command(p, "id", false, ""); argv[0] != a.Binary() {
			t.Fatalf("%s starts %q, not its binary %q", name, argv[0], a.Binary())
		}
	}
	if len(builtin) != len(Agents()) {
		t.Fatalf("%d built-in profiles for %d agents", len(builtin), len(Agents()))
	}
	for relayName, want := range map[string]string{"pi": "pi", "pi-2": "pi", "codex-3": "codex", "claude": "claude", "antigravity-x": "antigravity", "other": "", "pipeline": "", "pipeline-2": "", "codexer": "", "claude-fast": "claude"} {
		if got := AgentOf(relayName); got != want {
			t.Fatalf("AgentOf(%s) = %q", relayName, got)
		}
	}
	if _, err := agentFor("nope"); err == nil || !strings.Contains(err.Error(), "antigravity, claude, codex, pi") {
		t.Fatalf("unknown agent error %v", err)
	}
	// The dialogs every seat answers, in the order they always were.
	var names []string
	for _, d := range allDialogs() {
		names = append(names, d.name)
	}
	if strings.Join(names, ",") != "antigravity-trust,claude-trust,claude-bypass,codex-trust,codex-update,pi-trust" {
		t.Fatalf("dialogs %v", names)
	}
}

// A label is made on eagent's loop at every start, restart and reattach of
// an agent. pi's models read as their ids, so naming one never runs
// `pi --list-models` (a subprocess of up to eight seconds).
func TestPiLabelNeverRunsPi(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	// ": >" is the shell's own: with only dir on the PATH, touch would not
	// be found and the marker would never be written.
	script := "#!/bin/sh\n: > " + marker + "\n"
	if err := os.WriteFile(filepath.Join(dir, "pi"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	piCatalog.Lock()
	saved, savedRetry := piCatalog.models, piCatalog.retry
	piCatalog.models, piCatalog.retry = nil, time.Time{}
	piCatalog.Unlock()
	defer func() {
		piCatalog.Lock()
		piCatalog.models, piCatalog.retry = saved, savedRetry
		piCatalog.Unlock()
	}()
	if got := ModelLabel("pi", "", "anthropic/claude-x"); got != "anthropic/claude-x" {
		t.Fatalf("ModelLabel = %q", got)
	}
	if got := Label("pi", "", "anthropic/claude-x", "high"); got != "Pi · anthropic/claude-x high" {
		t.Fatalf("Label = %q", got)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("naming a model ran pi")
	}
}
