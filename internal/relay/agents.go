package relay

import (
	"cmp"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/ericflo/eagent/internal/config"
)

// Agent is one coding agent a relay can run: everything that differs
// between Claude Code, Codex, Antigravity, pi and the rest lives behind it.
// Adding an agent is one file that implements Agent (embedding agentBase
// for the optional parts) and calls register from its init, plus a line in
// config.BuiltinRelayProfiles when it should be offered by default.
//
// The exported methods are what the rest of eagent reads; the unexported
// ones are for seats and the choice helpers in this package.
type Agent interface {
	// Name is the agent's key: a profile's Agent field ("claude").
	Name() string
	// Label is its short name in messages ("Claude": "typed into Claude").
	Label() string
	// DisplayName is how people call it ("Claude Code").
	DisplayName() string
	// Binary is the program the relay starts ("claude").
	Binary() string

	// command is the argv that starts (or, with resume, continues) the
	// agent. hook is what prepare left in Spec.Hook.
	command(p config.RelayProfile, innerID string, resume bool, hook string) []string
	// locate finds the agent's transcript for this relay; "" until it
	// exists. innerID is the agent's own session id when known.
	locate(spec Spec, launched time.Time, innerID string) (path, id string)
	// newParser reads the transcript locate found.
	newParser(spec Spec) parser
	// afterTurn hands text to the agent to read when its current turn
	// ends. An error makes the seat type the text instead.
	afterTurn(s *Seat, id, text string) error

	// Optional, with defaults in agentBase:

	// chooseID returns the session id eagent picks up front, or "".
	chooseID() string
	// prepare fills in a relay's launch-time extras (hook, status file,
	// spool, files to write, environment). stateDir is a directory the
	// relay owns. It must not start anything: it also runs on reattach.
	prepare(spec *Spec, stateDir string) error
	// interruptKeys stop what the agent is doing.
	interruptKeys() []string
	// ready recognises the agent at its input prompt.
	ready(screen string) bool
	// dialogs are screens it can show that the relay answers itself.
	dialogs() []dialog
	// defaults are the model and effort it starts with when given none.
	defaults() (model, effort string)
	// models lists the models it can run, best first.
	models(project string) []ModelChoice
	// efforts lists the reasoning efforts it accepts, mildest first.
	efforts() []string
	// modelLabel is how one of its models reads in a relay's label.
	modelLabel(c ModelChoice) string
	// trimModelLabel shortens a model label that already says the effort.
	trimModelLabel(label, effort string) string
	// validModel says whether a model name is worth passing to it.
	validModel(model string) bool
	// checkPick refuses a model and effort it cannot run, before anything
	// stops or starts. p already carries the picks.
	checkPick(p config.RelayProfile) error
}

// Optional behaviours whose default needs the agent itself (embedding
// cannot reach the outer type), so they are separate interfaces.
type (
	// resolver fills a profile's model and effort in its own way; without
	// it, each one left empty comes from defaults.
	resolver interface {
		resolve(model, effort string) (string, string)
	}
	// upgrader names the agent's updater; without it, `<binary> update`.
	upgrader interface {
		upgradeCommand() []string
	}
	// modelSharer runs another agent's models and shares its suggestion
	// list (ModelListName), since a settings descriptor holds few lists.
	modelSharer interface {
		modelsOf() string
	}
)

// poller is an agent whose seat also reads something besides the
// transcript on every tick (a status file).
type poller interface {
	poll(s *Seat, now time.Time)
}

// dialogReporter is an agent that says itself when it waits on a dialog
// (pi's extension writes it into its status file). The seat reads it on
// every tick, also before a transcript exists, and holds the relay blocked
// until the dialog ends. It returns the dialog's title, "" when none is up.
type dialogReporter interface {
	reportedDialog(s *Seat) string
}

// agentBase gives an agent the default for every optional method. An
// agent embeds it and overrides what differs.
type agentBase struct{}

func (agentBase) chooseID() string                    { return "" }
func (agentBase) prepare(*Spec, string) error         { return nil }
func (agentBase) interruptKeys() []string             { return []string{"Escape"} }
func (agentBase) ready(string) bool                   { return false }
func (agentBase) dialogs() []dialog                   { return nil }
func (agentBase) defaults() (string, string)          { return "", "" }
func (agentBase) models(string) []ModelChoice         { return nil }
func (agentBase) efforts() []string                   { return nil }
func (agentBase) modelLabel(c ModelChoice) string     { return c.Label }
func (agentBase) trimModelLabel(l, _ string) string   { return l }
func (agentBase) validModel(m string) bool            { return !strings.Contains(m, " ") }
func (agentBase) checkPick(config.RelayProfile) error { return nil }

// resolve is ProfileDefaults for one agent.
func resolve(a Agent, model, effort string) (string, string) {
	if r, ok := a.(resolver); ok {
		return r.resolve(model, effort)
	}
	return resolveDefaults(a, model, effort)
}

// resolveDefaults fills each value left empty from the agent's defaults.
func resolveDefaults(a Agent, model, effort string) (string, string) {
	defModel, defEffort := a.defaults()
	return cmp.Or(model, defModel), cmp.Or(effort, defEffort)
}

// ---- registry -----------------------------------------------------------------

var registry = map[string]Agent{}

// register adds an agent; each agent's file calls it from init.
func register(a Agent) {
	if a.Name() == "" || registry[a.Name()] != nil {
		panic("relay: bad or duplicate agent " + a.Name())
	}
	registry[a.Name()] = a
}

// Agents lists every agent a relay can run, by name.
func Agents() []Agent {
	out := make([]Agent, 0, len(registry))
	for _, a := range registry {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// AgentNames lists the agents' names, sorted.
func AgentNames() []string {
	var names []string
	for _, a := range Agents() {
		names = append(names, a.Name())
	}
	return names
}

// AgentByName finds an agent; nil when there is none by that name.
func AgentByName(name string) Agent { return registry[name] }

func agentFor(name string) (Agent, error) {
	if a := AgentByName(name); a != nil {
		return a, nil
	}
	return nil, fmt.Errorf("unknown relay agent %q (%s)", name, strings.Join(AgentNames(), ", "))
}

// AgentLabel is the short name of an agent program ("Claude").
func AgentLabel(name string) string {
	if a := AgentByName(name); a != nil {
		return a.Label()
	}
	return name
}

// Installed reports whether an agent's program is on this machine.
func Installed(agent string) bool {
	a := AgentByName(agent)
	if a == nil {
		return false
	}
	_, err := exec.LookPath(a.Binary())
	return err == nil
}

// AgentOf reads which agent a relay runs from its name ("codex-2" runs
// codex), for relays found running without their session's log: the name
// is the agent's, or the agent's followed by "-" and more. The longest
// such agent name wins; "" when none does, so a custom profile named
// "pipeline" is not taken for pi.
func AgentOf(relayName string) string {
	best := ""
	for _, a := range Agents() {
		n := a.Name()
		if (relayName == n || strings.HasPrefix(relayName, n+"-")) && len(n) > len(best) {
			best = a.Name()
		}
	}
	return best
}

// Prepare fills in what the profile's agent needs at launch (a hook, a
// status file, a spool). stateDir is a directory the relay owns. An
// unknown agent is left for Launch to refuse.
func Prepare(spec *Spec, stateDir string) error {
	if a := AgentByName(spec.Profile.Agent); a != nil {
		return a.prepare(spec, stateDir)
	}
	return nil
}

// CheckPick refuses a session's model and effort picks that the profile's
// agent cannot take, before anything starts.
func CheckPick(p config.RelayProfile, model, effort string) error {
	if efforts := AgentEfforts(p.Agent); effort != "" && len(efforts) > 0 && !slices.Contains(efforts, effort) {
		return fmt.Errorf("%s takes reasoning effort %s, not %q", AgentName(p.Agent), strings.Join(efforts, ", "), effort)
	}
	if !pickWord(effort) {
		return fmt.Errorf("%q is not a reasoning effort", effort)
	}
	a := AgentByName(p.Agent)
	if len(model) > 200 || !pickWord(model) || a != nil && !a.validModel(model) || a == nil && strings.Contains(model, " ") {
		return fmt.Errorf("%q is not a model name", model)
	}
	if a == nil {
		return nil
	}
	return a.checkPick(p.With(model, effort))
}

// pickWord reports whether a model or effort pick can go on an agent's
// command line and into its terminal as a value: no control character (a
// newline would submit, an escape would end a paste), nothing starting
// with "-" (the agent would read a flag) and no ";" (no model name has one,
// and terminal multiplexers split commands at it).
func pickWord(s string) bool {
	return !strings.HasPrefix(s, "-") && !strings.Contains(s, ";") && !strings.ContainsFunc(s, unicode.IsControl)
}

// UpgradeCommand is the argv that updates an agent's CLI.
func UpgradeCommand(agent string) ([]string, error) {
	a := AgentByName(agent)
	if a == nil {
		return nil, fmt.Errorf("no upgrade command for %s", agent)
	}
	if u, ok := a.(upgrader); ok {
		return u.upgradeCommand(), nil
	}
	return []string{a.Binary(), "update"}, nil
}

// allDialogs are every agent's dialogs, in agent order. A seat answers any
// of them, whichever agent it runs.
func allDialogs() []dialog {
	var out []dialog
	for _, a := range Agents() {
		out = append(out, a.dialogs()...)
	}
	return out
}

// ---- shared helpers -------------------------------------------------------------

// claimed holds transcripts already followed by a relay in this process, so
// two relays started in one directory never follow the same file.
var claimed sync.Map

// newUUID is a random (version 4) UUID, for agents whose session id eagent
// picks.
func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// spoolMessage leaves text in a relay's spool, where the agent's own hook
// picks it up when its turn ends. The write is atomic: a reader sees the
// whole file or none.
func spoolMessage(spool, id, text string) error {
	if spool == "" {
		return errors.New("no hook spool for this relay")
	}
	if err := os.MkdirAll(spool, 0o755); err != nil {
		return err
	}
	name := fmt.Sprintf("%d-%s.txt", time.Now().UnixNano(), id)
	tmp := filepath.Join(spool, "."+name)
	if err := os.WriteFile(tmp, []byte(text), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(spool, name))
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
