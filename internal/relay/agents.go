package relay

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ericflo/eagent/internal/config"
)

// agent is what differs between the programs a relay can run.
type agent interface {
	// command is the argv that starts (or, with resume, continues) the agent.
	command(p config.RelayProfile, innerID string, resume bool, hook string) []string
	// locate finds the agent's transcript for this relay; "" until it exists.
	locate(dir string, launched time.Time, innerID string) (path, id string)
	newParser() parser
	// afterTurn hands text to the agent to read when its current turn ends.
	afterTurn(s *Seat, id, text string) error
	// chooseID returns the session id eagent picks up front, or "".
	chooseID() string
	label() string
}

func agentFor(name string) (agent, error) {
	switch name {
	case "codex":
		return codexAgent{}, nil
	case "claude":
		return claudeAgent{}, nil
	}
	return nil, fmt.Errorf("unknown relay agent %q (codex or claude)", name)
}

// AgentLabel is the human name of an agent program.
func AgentLabel(name string) string {
	if a, err := agentFor(name); err == nil {
		return a.label()
	}
	return name
}

// claimed holds transcripts already followed by a relay in this process, so
// two relays started in one directory never follow the same file.
var claimed sync.Map

// ---- Codex ------------------------------------------------------------------

type codexAgent struct{}

func (codexAgent) label() string     { return "Codex" }
func (codexAgent) chooseID() string  { return "" }
func (codexAgent) newParser() parser { return codexParser{} }

func (codexAgent) command(p config.RelayProfile, innerID string, resume bool, _ string) []string {
	argv := []string{"codex"}
	if resume && innerID != "" {
		argv = append(argv, "resume", innerID)
	}
	argv = append(argv, p.Args...)
	if p.Model != "" {
		argv = append(argv, "-m", p.Model)
	}
	if p.Effort != "" {
		argv = append(argv, "-c", fmt.Sprintf("model_reasoning_effort=%q", p.Effort))
	}
	return argv
}

func codexHome() string {
	if h := os.Getenv("CODEX_HOME"); h != "" {
		return h
	}
	return filepath.Join(home(), ".codex")
}

// locate scans the rollout folders for the days around the launch for the
// TUI session that started in dir after the launch (or, on a resume, the
// newest rollout of the known id).
func (codexAgent) locate(dir string, launched time.Time, innerID string) (string, string) {
	root := filepath.Join(codexHome(), "sessions")
	want := cleanDir(dir)
	type cand struct {
		path, id string
		start    time.Time
		mod      time.Time
	}
	var cands []cand
	for _, day := range []time.Time{launched.Add(-24 * time.Hour), launched, launched.Add(24 * time.Hour)} {
		folder := filepath.Join(root, day.Format("2006"), day.Format("01"), day.Format("02"))
		entries, err := os.ReadDir(folder)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !strings.HasPrefix(e.Name(), "rollout-") || !strings.HasSuffix(e.Name(), ".jsonl") {
				continue
			}
			info, err := e.Info()
			if err != nil || info.ModTime().Before(launched.Add(-5*time.Second)) {
				continue
			}
			path := filepath.Join(folder, e.Name())
			if _, taken := claimed.Load(path); taken {
				continue
			}
			meta, ok := codexMeta(path)
			if !ok {
				continue
			}
			if innerID != "" {
				if meta.ID == innerID {
					cands = append(cands, cand{path, meta.ID, meta.start, info.ModTime()})
				}
				continue
			}
			if cleanDir(meta.Cwd) != want || meta.start.Before(launched.Add(-10*time.Second)) {
				continue
			}
			cands = append(cands, cand{path, meta.ID, meta.start, info.ModTime()})
		}
	}
	if len(cands) == 0 {
		return "", ""
	}
	if innerID != "" {
		sort.Slice(cands, func(i, j int) bool { return cands[i].mod.After(cands[j].mod) })
	} else {
		sort.Slice(cands, func(i, j int) bool { return cands[i].start.Before(cands[j].start) })
	}
	return cands[0].path, cands[0].id
}

type rolloutMeta struct {
	ID, Cwd string
	start   time.Time
}

func codexMeta(path string) (rolloutMeta, bool) {
	f, err := os.Open(path)
	if err != nil {
		return rolloutMeta{}, false
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 256<<10)
	line, err := r.ReadSlice('\n')
	if err != nil && !errors.Is(err, bufio.ErrBufferFull) && !errors.Is(err, io.EOF) {
		return rolloutMeta{}, false
	}
	// The meta line carries the whole base prompt; only its head is needed,
	// and a cut-off line still has the fields that come first.
	var ev struct {
		Type    string `json:"type"`
		Payload struct {
			ID        string `json:"id"`
			Cwd       string `json:"cwd"`
			Timestamp string `json:"timestamp"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &ev) != nil {
		head := string(line)
		ev.Type = between(head, `"type":"`, `"`)
		ev.Payload.ID = between(head, `"id":"`, `"`)
		ev.Payload.Cwd = between(head, `"cwd":"`, `"`)
		ev.Payload.Timestamp = between(head[strings.Index(head, `"payload"`)+1:], `"timestamp":"`, `"`)
	}
	if ev.Type != "session_meta" || ev.Payload.ID == "" {
		return rolloutMeta{}, false
	}
	return rolloutMeta{ID: ev.Payload.ID, Cwd: ev.Payload.Cwd, start: parseTime(ev.Payload.Timestamp)}, true
}

func between(s, open, close string) string {
	i := strings.Index(s, open)
	if i < 0 {
		return ""
	}
	s = s[i+len(open):]
	if j := strings.Index(s, close); j >= 0 {
		return s[:j]
	}
	return ""
}

// afterTurn uses Codex's own queue: the message becomes the next turn.
func (codexAgent) afterTurn(s *Seat, _ string, text string) error {
	id := s.innerID()
	if id == "" {
		return errors.New("Codex's session id is not known yet")
	}
	out, err := exec.Command("codex", "queue", "--thread", id, "--message", text).CombinedOutput()
	if err != nil {
		return fmt.Errorf("codex queue: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ---- Claude Code --------------------------------------------------------------

type claudeAgent struct{}

func (claudeAgent) label() string     { return "Claude" }
func (claudeAgent) newParser() parser { return &claudeParser{} }

func (claudeAgent) chooseID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func (claudeAgent) command(p config.RelayProfile, innerID string, resume bool, hook string) []string {
	argv := []string{"claude"}
	if resume {
		argv = append(argv, "--resume", innerID)
	} else {
		argv = append(argv, "--session-id", innerID)
	}
	if p.Model != "" {
		argv = append(argv, "--model", p.Model)
	}
	if p.Effort != "" {
		argv = append(argv, "--effort", p.Effort)
	}
	if hook != "" {
		settings, _ := json.Marshal(map[string]any{"hooks": map[string]any{"Stop": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": hook}}}}}})
		argv = append(argv, "--settings", string(settings))
	}
	return append(argv, p.Args...)
}

func claudeHome() string {
	if h := os.Getenv("CLAUDE_CONFIG_DIR"); h != "" {
		return h
	}
	return filepath.Join(home(), ".claude")
}

func (claudeAgent) locate(_ string, _ time.Time, innerID string) (string, string) {
	if innerID == "" {
		return "", ""
	}
	matches, _ := filepath.Glob(filepath.Join(claudeHome(), "projects", "*", innerID+".jsonl"))
	if len(matches) == 0 {
		return "", ""
	}
	return matches[0], innerID
}

// afterTurn leaves the text in the spool the Stop hook reads: when Claude's
// turn ends, the hook hands it over as the next thing to do.
func (claudeAgent) afterTurn(s *Seat, id, text string) error {
	if s.spec.Spool == "" {
		return errors.New("no hook spool for this relay")
	}
	if err := os.MkdirAll(s.spec.Spool, 0o755); err != nil {
		return err
	}
	name := fmt.Sprintf("%d-%s.txt", time.Now().UnixNano(), id)
	tmp := filepath.Join(s.spec.Spool, "."+name)
	if err := os.WriteFile(tmp, []byte(text), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(s.spec.Spool, name))
}

// RunStopHook is Claude Code's Stop hook for a relay: it takes every message
// waiting in spool and returns them as the reason to keep going, which
// Claude reads as its next instruction. With nothing waiting it lets the
// turn end.
func RunStopHook(spool string, stdin io.Reader, stdout io.Writer) int {
	_, _ = io.Copy(io.Discard, io.LimitReader(stdin, 1<<20))
	entries, err := os.ReadDir(spool)
	if err != nil {
		return 0
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".txt") && !strings.HasPrefix(e.Name(), ".") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	var texts []string
	for _, name := range names {
		path := filepath.Join(spool, name)
		taken := path + ".taken"
		if os.Rename(path, taken) != nil {
			continue // another hook got it
		}
		raw, err := os.ReadFile(taken)
		if err == nil && strings.TrimSpace(string(raw)) != "" {
			texts = append(texts, strings.TrimSpace(string(raw)))
		}
		_ = os.Remove(taken)
	}
	if len(texts) == 0 {
		return 0
	}
	out, _ := json.Marshal(map[string]string{"decision": "block", "reason": strings.Join(texts, "\n\n")})
	fmt.Fprintln(stdout, string(out))
	return 0
}

func cleanDir(dir string) string {
	if dir == "" {
		return ""
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	return filepath.Clean(dir)
}

// ShellQuote quotes one argument for a POSIX shell.
func ShellQuote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./=:@,+", r))
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
