package relay

import (
	"cmp"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ericflo/eagent/internal/config"
)

// pi (pi-coding-agent). eagent picks the session id up front and passes it
// with --session-id, which also resumes it. pi's transcript cannot say for
// sure when a run is over (it retries, compacts and runs queued follow-ups
// after an answer that looks final), so eagent loads a small extension of
// its own (piExtension) that marks the exact end of every run in the
// transcript, keeps a status file saying which session file pi writes, and
// hands over messages held for the end of a turn.
//
// piAgent is parameterised so an agent built on pi (a wrapper binary, a
// different home) can reuse it: embed it, or register another value.
type piAgent struct {
	agentBase
	name, label, binary string
}

func init() { register(piAgent{name: "pi", label: "Pi", binary: "pi"}) }

func (a piAgent) Name() string        { return a.name }
func (a piAgent) Label() string       { return a.label }
func (a piAgent) DisplayName() string { return a.label }
func (a piAgent) Binary() string      { return a.binary }

func (piAgent) chooseID() string { return newUUID() }

func (a piAgent) newParser(spec Spec) parser { return &piParser{statusFile: spec.StatusFile} }

// The custom entries piExtension writes into pi's session file.
const (
	piSettledEntry = "eagent-settled" // a run is over: {aborted}
	piPromptEntry  = "eagent-prompt"  // pi waits on a dialog: {kind, title}
)

// piExtension is loaded into pi with -e. It reads the status file and the
// spool from EAGENT_PI_STATUS and EAGENT_PI_SPOOL. pi loads TypeScript
// through jiti; this is plain JavaScript with a few type annotations.
const piExtension = `// eagent's relay extension for pi. eagent writes this file when it starts pi
// for a relay and loads it with -e. It does three things:
//   - marks the end of every run in the session file (a custom entry named
//     eagent-settled), so eagent knows exactly when pi has finished,
//     retries, compaction and queued follow-ups included;
//   - keeps a small status file (EAGENT_PI_STATUS) saying which session file
//     pi writes, whether it is working, and any dialog it is waiting on;
//   - hands over messages eagent held for the end of a turn: files in the
//     spool directory (EAGENT_PI_SPOOL), sent as the next user message.
import * as fs from "node:fs";
import * as path from "node:path";

export default function (pi: any) {
	const statusFile = process.env.EAGENT_PI_STATUS || "";
	const spool = process.env.EAGENT_PI_SPOOL || "";
	const status: any = { state: "starting", aborted: false, prompt: "", session_file: "", session_id: "", settled: 0, at: "" };
	let timer: any;
	let prompting = false;

	function save(change: any) {
		Object.assign(status, change, { at: new Date().toISOString() });
		if (!statusFile) return;
		try {
			fs.mkdirSync(path.dirname(statusFile), { recursive: true });
			const tmp = statusFile + "." + process.pid + ".tmp";
			fs.writeFileSync(tmp, JSON.stringify(status) + "\n");
			fs.renameSync(tmp, statusFile);
		} catch {}
	}

	// take claims every waiting message (renaming it first, so no message
	// is handed over twice) and returns them joined, oldest first.
	function take(): string {
		if (!spool) return "";
		let names: string[];
		try {
			names = fs.readdirSync(spool).filter((n) => n.endsWith(".txt") && !n.startsWith(".")).sort();
		} catch {
			return "";
		}
		const texts: string[] = [];
		for (const name of names) {
			const file = path.join(spool, name);
			const taken = file + ".taken";
			try {
				fs.renameSync(file, taken);
			} catch {
				continue;
			}
			try {
				const text = fs.readFileSync(taken, "utf8").trim();
				if (text) texts.push(text);
			} catch {}
			try {
				fs.unlinkSync(taken);
			} catch {}
		}
		return texts.join("\n\n");
	}

	function deliver() {
		if (prompting) return;
		const text = take();
		if (text) pi.sendUserMessage(text);
	}

	pi.on("session_start", (_event: any, ctx: any) => {
		save({ state: "idle", session_file: ctx.sessionManager.getSessionFile() || "", session_id: ctx.sessionManager.getSessionId() || "" });
		// A message held just as a run ended waits here until pi is idle.
		clearInterval(timer);
		timer = setInterval(() => {
			try {
				if (status.state === "idle" && ctx.isIdle() && !ctx.hasPendingMessages()) deliver();
			} catch {}
		}, 1000);
		if (timer && timer.unref) timer.unref();
	});
	pi.on("session_shutdown", () => {
		clearInterval(timer);
	});
	pi.on("agent_start", () => {
		save({ state: "working", aborted: false });
	});
	pi.on("agent_settled", (event: any) => {
		const aborted = !!(event && event.aborted);
		try {
			pi.appendEntry("` + piSettledEntry + `", { aborted });
		} catch {}
		save({ state: "idle", aborted, settled: status.settled + 1 });
		deliver();
	});
	pi.on("ui_prompt_start", (event: any) => {
		prompting = true;
		const title = (event && (event.title || event.kind)) || "a dialog";
		try {
			pi.appendEntry("` + piPromptEntry + `", { kind: event && event.kind, title });
		} catch {}
		save({ prompt: title });
	});
	pi.on("ui_prompt_end", () => {
		prompting = false;
		save({ prompt: "" });
	});
}
`

// prepare sets up the extension, its status file and the spool, and keeps
// pi's update notice off the relay's screen.
func (piAgent) prepare(spec *Spec, stateDir string) error {
	spec.StatusFile = filepath.Join(stateDir, "pi-status.json")
	spec.Spool = filepath.Join(stateDir, "after_turn")
	spec.Hook = filepath.Join(stateDir, "eagent-relay.ts")
	spec.Files = map[string]string{spec.Hook: piExtension}
	spec.Env = append(spec.Env, "PI_SKIP_VERSION_CHECK=1", "EAGENT_PI_STATUS="+spec.StatusFile, "EAGENT_PI_SPOOL="+spec.Spool)
	return nil
}

// command is the same for a start and a resume: --session-id opens the
// session with that id in the directory, or creates it. --approve trusts
// project files for this run, so pi never stops on its trust prompt; pi
// has no permission prompts to skip.
func (a piAgent) command(p config.RelayProfile, innerID string, _ bool, hook string) []string {
	argv := []string{a.binary}
	if innerID != "" {
		argv = append(argv, "--session-id", innerID)
	}
	if p.Model != "" {
		argv = append(argv, "--model", p.Model)
	}
	if p.Effort != "" {
		argv = append(argv, "--thinking", p.Effort)
	}
	argv = append(argv, "--approve")
	if hook != "" {
		argv = append(argv, "-e", hook)
	}
	return append(argv, p.Args...)
}

// afterTurn leaves the text in the spool; the extension sends it as the
// next message once pi has settled.
func (piAgent) afterTurn(s *Seat, id, text string) error {
	return spoolMessage(s.spec.Spool, id, text)
}

// Interrupt uses the default single Escape. Never Ctrl+C: pi only clears
// its editor on one and exits on two. Escape also puts any messages typed
// while pi worked (and not yet read) back into the editor; the seat's
// stuck-send check presses Enter on them, or they go with the next message.

var piFooter = regexp.MustCompile(`\d+(?:\.\d+)?%/\d+(?:\.\d+)?[kKmMgG]? \(`)

// ready matches pi's footer (context use, "0.4%/1.0M (auto)") or the key
// hints at the top of a fresh screen.
func (piAgent) ready(screen string) bool {
	return piFooter.MatchString(screen) || strings.Contains(screen, "escape interrupt · ctrl+c/ctrl+d clear/exit")
}

func (piAgent) dialogs() []dialog {
	return []dialog{
		// Only shown without --approve: trust the folder for this run only.
		{name: "pi-trust", match: []string{"Trust project folder?", "→ Trust", "Trust (this session only)"}, keys: []string{"Down", "Down", "Enter"}, what: "trusted the folder for this pi session"},
	}
}

func (a piAgent) upgradeCommand() []string { return []string{a.binary, "update", "--all"} }

// piHome is pi's agent directory.
func piHome() string {
	if h := os.Getenv("PI_CODING_AGENT_DIR"); h != "" {
		return ExpandHome(h)
	}
	return filepath.Join(home(), ".pi", "agent")
}

// piStatus is what the extension saves.
type piStatus struct {
	State       string `json:"state"`
	Aborted     bool   `json:"aborted"`
	Prompt      string `json:"prompt"`
	SessionFile string `json:"session_file"`
	SessionID   string `json:"session_id"`
	Settled     int    `json:"settled"`
	At          string `json:"at"`
}

func readPiStatus(file string) (piStatus, bool) {
	var st piStatus
	if file == "" {
		return st, false
	}
	raw, err := os.ReadFile(file)
	if err != nil || json.Unmarshal(raw, &st) != nil {
		return st, false
	}
	return st, true
}

// piSessionDir is where pi keeps a directory's sessions:
// sessions/--<absolute path, / \ and : as ->--. pi does not resolve
// symlinks, so neither does this.
func piSessionDir(dir string) string {
	if d := os.Getenv("PI_CODING_AGENT_SESSION_DIR"); d != "" {
		return ExpandHome(d)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	safe := strings.NewReplacer("/", "-", "\\", "-", ":", "-").Replace(strings.TrimLeft(abs, `/\`))
	return filepath.Join(piHome(), "sessions", "--"+safe+"--")
}

// locate trusts the extension's status file first: it names the session
// file pi writes now, also after /new or /resume. Without it, the file is
// found by id: <created>_<id>.jsonl in the directory's session folder. pi
// writes the file only once the first message exists, so this is "" until
// then.
func (piAgent) locate(spec Spec, _ time.Time, innerID string) (string, string) {
	if st, ok := readPiStatus(spec.StatusFile); ok && st.SessionFile != "" && st.SessionID != "" {
		if _, err := os.Stat(st.SessionFile); err == nil {
			return st.SessionFile, st.SessionID
		}
		return "", ""
	}
	if innerID == "" {
		return "", ""
	}
	name := "*_" + globEscape(innerID) + ".jsonl"
	for _, pattern := range []string{
		filepath.Join(globEscape(piSessionDir(spec.Dir)), name),
		filepath.Join(globEscape(filepath.Join(piHome(), "sessions")), "*", name),
	} {
		if matches, _ := filepath.Glob(pattern); len(matches) > 0 {
			sort.Strings(matches)
			return matches[len(matches)-1], innerID
		}
	}
	return "", ""
}

func globEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, "*", `\*`, "?", `\?`, "[", `\[`).Replace(s)
}

// poll follows pi to another session of its own (/new, /resume, /fork):
// the status file then names a session the seat does not follow.
func (piAgent) poll(s *Seat, _ time.Time) {
	st, ok := readPiStatus(s.spec.StatusFile)
	if !ok || st.SessionID == "" {
		return
	}
	if inner := s.innerID(); inner != "" && inner != st.SessionID {
		s.follow(st.SessionID)
	}
}

// reportedDialog is the dialog the extension says pi waits on (its
// ui_prompt_start, cleared at ui_prompt_end): the seat shows the relay
// blocked on it until it ends.
func (piAgent) reportedDialog(s *Seat) string {
	st, ok := readPiStatus(s.spec.StatusFile)
	if !ok {
		return ""
	}
	return strings.TrimSpace(st.Prompt)
}

// ---- choices ------------------------------------------------------------------

// piEfforts are pi's thinking levels (--thinking), clamped by pi to what
// the model supports.
var piEfforts = []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}

func (piAgent) efforts() []string { return slices.Clone(piEfforts) }

// plainModelIDs: pi's models are provider/model ids with no other label.
func (piAgent) plainModelIDs() {}

// defaults are settings.json's defaultProvider/defaultModel and
// defaultThinkingLevel.
func (piAgent) defaults() (string, string) {
	var s struct {
		Provider string `json:"defaultProvider"`
		Model    string `json:"defaultModel"`
		Thinking string `json:"defaultThinkingLevel"`
	}
	raw, err := os.ReadFile(filepath.Join(piHome(), "settings.json"))
	if err != nil || json.Unmarshal(raw, &s) != nil {
		return "", ""
	}
	model := strings.TrimSpace(s.Model)
	if p := strings.TrimSpace(s.Provider); p != "" && model != "" {
		model = p + "/" + model
	}
	return model, strings.TrimSpace(s.Thinking)
}

// maxPiModels bounds pi's list: it knows hundreds of models. The settings
// descriptor holds at most 5000 suggestions over all its lists.
const maxPiModels = 300

// piCatalog caches `pi --list-models` (it takes about half a second).
var piCatalog struct {
	sync.Mutex
	models []ModelChoice
	retry  time.Time
}

// models lists `pi --offline --list-models` as provider/model ids: the
// default provider's first, then providers with fewer models (a local
// server, a custom provider) before the big catalogues.
func (a piAgent) models(string) []ModelChoice {
	piCatalog.Lock()
	defer piCatalog.Unlock()
	if time.Now().After(piCatalog.retry) {
		cmd, cancel := bounded(8*time.Second, a.binary, "--offline", "--list-models")
		cmd.Env = append(os.Environ(), "PI_SKIP_VERSION_CHECK=1")
		raw, err := cmd.Output()
		cancel()
		piCatalog.retry = time.Now().Add(time.Minute)
		if err == nil {
			defModel, _ := a.defaults()
			defProvider, _, _ := strings.Cut(defModel, "/")
			if models := parsePiModels(string(raw), defProvider); len(models) > 0 {
				piCatalog.models = models
				piCatalog.retry = time.Now().Add(time.Hour)
			}
		}
	}
	return slices.Clone(piCatalog.models)
}

// parsePiModels reads the table --list-models prints: a header, then
// "provider model context max-out thinking images" per line.
func parsePiModels(table, defProvider string) []ModelChoice {
	type row struct{ provider, model, detail string }
	var rows []row
	count := map[string]int{}
	for _, line := range strings.Split(table, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || f[0] == "provider" && f[1] == "model" {
			continue
		}
		detail := ""
		if len(f) >= 4 {
			detail = f[2] + " context, " + f[3] + " output"
		}
		if len(f) >= 5 && f[4] == "yes" {
			detail += ", thinking"
		}
		rows = append(rows, row{f[0], f[1], detail})
		count[f[0]]++
	}
	rank := func(p string) int {
		if p == defProvider {
			return -1
		}
		return count[p]
	}
	sort.SliceStable(rows, func(i, j int) bool { return rank(rows[i].provider) < rank(rows[j].provider) })
	l := choiceList{max: maxPiModels}
	for _, r := range rows {
		l.add(ModelChoice{ID: r.provider + "/" + r.model, Detail: r.detail})
	}
	return l.out
}

// ---- transcript -----------------------------------------------------------------

// piParser reads pi's session file. With eagent's extension loaded (its
// status file exists), a run ends only at the extension's eagent-settled
// entry. Without it, an assistant message that stopped or was aborted ends
// the turn, which a retry or a queued follow-up can prove wrong.
type piParser struct {
	statusFile string
	exact      bool   // the extension is marking run ends
	held       string // the latest assistant text: progress, or the turn's report
	failed     string // the error the run last stopped on, until it moves on
}

func (p *piParser) marked() bool {
	if !p.exact && p.statusFile != "" {
		if _, err := os.Stat(p.statusFile); err == nil {
			p.exact = true
		}
	}
	return p.exact
}

func (p *piParser) line(raw []byte) []obs {
	var ev struct {
		Type       string          `json:"type"`
		ID         string          `json:"id"`
		Timestamp  string          `json:"timestamp"`
		CustomType string          `json:"customType"`
		Data       json.RawMessage `json:"data"`
		Message    *struct {
			Role         string          `json:"role"`
			Content      json.RawMessage `json:"content"`
			StopReason   string          `json:"stopReason"`
			ErrorMessage string          `json:"errorMessage"`
		} `json:"message"`
	}
	if json.Unmarshal(raw, &ev) != nil {
		return nil
	}
	at := parseTime(ev.Timestamp)
	switch ev.Type {
	case "session":
		return []obs{{kind: "meta", id: ev.ID, at: at}}
	case "compaction":
		return []obs{{kind: "compact", at: at}}
	case "custom":
		switch ev.CustomType {
		case piSettledEntry:
			var d struct {
				Aborted bool `json:"aborted"`
			}
			_ = json.Unmarshal(ev.Data, &d)
			p.exact = true
			return p.settled(d.Aborted, at)
		}
		// piPromptEntry is left alone here: the status file says when a
		// dialog is up and when it ends (reportedDialog), and the seat
		// holds the relay blocked meanwhile. As progress it made pi look
		// busy instead.
		return nil
	case "message":
	default:
		return nil
	}
	if ev.Message == nil {
		return nil
	}
	m := ev.Message
	switch m.Role {
	case "user":
		p.held, p.failed = "", ""
		return []obs{{kind: "user", text: piText(m.Content), at: at}, {kind: "working", at: at}}
	case "assistant":
	default:
		return nil
	}
	var parts []struct {
		Type      string         `json:"type"`
		Text      string         `json:"text"`
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	_ = json.Unmarshal(m.Content, &parts)
	var out []obs
	for _, part := range parts {
		switch part.Type {
		case "text":
			t := strings.TrimSpace(part.Text)
			if t == "" {
				continue
			}
			if p.held != "" {
				out = append(out, obs{kind: "progress", text: p.held, at: at})
			}
			p.held = t
		case "toolCall":
			if p.held != "" {
				out = append(out, obs{kind: "progress", text: p.held, at: at})
				p.held = ""
			}
			out = append(out, piTool(part.Name, part.Arguments, at)...)
		}
	}
	p.failed = ""
	switch m.StopReason {
	case "stop":
		if !p.marked() {
			text := p.held
			p.held = ""
			out = append(out, obs{kind: "turn", text: text, at: at})
		}
	case "aborted":
		if !p.marked() {
			text := p.held
			p.held = ""
			out = append(out, obs{kind: "aborted", text: text, at: at})
		}
	case "error":
		if p.marked() {
			p.failed = cmp.Or(m.ErrorMessage, "error")
			break
		}
		out = append(out, obs{kind: "aborted", text: m.ErrorMessage, at: at})
		if l := limitText(m.ErrorMessage); l != "" {
			out = append(out, obs{kind: "limit", exhausted: true, text: l, at: at})
		}
		p.held = ""
	}
	return out
}

// settled ends the run the extension marked. A run that gave up on an
// error reports the error.
func (p *piParser) settled(aborted bool, at time.Time) []obs {
	text, failed := p.held, p.failed
	p.held, p.failed = "", ""
	var out []obs
	if failed != "" {
		if l := limitText(failed); l != "" {
			out = append(out, obs{kind: "limit", exhausted: true, text: l, at: at})
		}
		if text == "" {
			text = failed
		}
		aborted = true
	}
	kind := "turn"
	if aborted {
		kind = "aborted"
	}
	return append(out, obs{kind: kind, text: text, at: at})
}

// piText is a user message's text: a string, or the text parts of an
// array (images are left out).
func piText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	_ = json.Unmarshal(raw, &parts)
	var texts []string
	for _, part := range parts {
		if part.Type == "text" {
			texts = append(texts, part.Text)
		}
	}
	return strings.Join(texts, "\n")
}

func piTool(name string, args map[string]any, at time.Time) []obs {
	str := func(k string) string { s, _ := args[k].(string); return s }
	first := func(k string) string {
		if s := str(k); s != "" {
			return s
		}
		if list, ok := args[k].([]any); ok && len(list) > 0 {
			if s, ok := list[0].(string); ok {
				return s
			}
		}
		return ""
	}
	switch name {
	case "bash":
		return []obs{{kind: "activity", text: "running " + oneLine(str("command"), 120), at: at, tool: true}}
	case "read":
		if path := str("path"); isImagePath(path) {
			return []obs{{kind: "image", text: path, at: at, tool: true}}
		}
		return []obs{{kind: "activity", text: "reading " + filepath.Base(str("path")), at: at, tool: true}}
	case "edit", "write":
		path := str("path")
		out := []obs{{kind: "activity", text: "editing " + filepath.Base(path), at: at, tool: true}}
		if isImagePath(path) {
			out = append(out, obs{kind: "image", text: path, at: at})
		}
		return out
	case "grep", "find":
		return []obs{{kind: "activity", text: "searching for " + oneLine(str("pattern"), 80), at: at, tool: true}}
	case "web_search":
		return []obs{{kind: "activity", text: "looking up " + oneLine(cmp.Or(first("queries"), first("query")), 100), at: at, tool: true}}
	case "fetch_content":
		return []obs{{kind: "activity", text: "looking up " + oneLine(cmp.Or(first("urls"), first("url")), 100), at: at, tool: true}}
	}
	return []obs{{kind: "activity", text: "using " + name, at: at, tool: true}}
}
