package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/ericflo/eagent/internal/procs"
	"github.com/ericflo/eagent/internal/settings"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/store"
)

// SessionStart reports a session started on behalf of a remote request.
type SessionStart struct {
	ID string
	// Host says where the session runs: "in_process" (an eagent server or
	// connector that keeps it interactive) or "detached" (a batch process of
	// its own that ends when the work is done).
	Host        string
	Interactive bool
	Message     string
}

// SessionRequest is what a remote start asks for: the first message and,
// optionally, the directory the session's commands start in (the project
// root by default).
type SessionRequest struct {
	Prompt string
	Dir    string
	// Relay names a relay profile ("codex", "claude"): the session starts
	// that agent in Dir and relays the user's messages to it.
	Relay string
	// Config is the preset (or "bundle:NAME") the session starts from, ""
	// for the project's own configuration; Routes are per-actor model picks
	// on top of it.
	Config string
	Routes map[string]RoutePick
}

// RoutePick is one actor's model pick for a new session. An empty provider
// is inferred from the model; a pick with only an effort keeps the model.
type RoutePick struct {
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	Effort   string `json:"effort,omitempty"`
}

// SplitConfig turns a session.start configuration choice into a preset or a
// bundle name.
func SplitConfig(name string) (preset, bundle string) {
	if b, ok := strings.CutPrefix(name, "bundle:"); ok {
		return "", b
	}
	return name, ""
}

// ApplyPicks puts per-actor model picks on a configuration, each through
// config.PickRoute (catalog or cached live models only, never the network).
func ApplyPicks(project string, cfg config.Config, picks map[string]RoutePick) (config.Config, error) {
	actors := map[string]*config.Actor{"orchestrator": &cfg.Orchestrator, "task": &cfg.Task, "narrator": &cfg.Narrator}
	for name, p := range picks {
		a, ok := actors[name]
		if !ok {
			return cfg, fmt.Errorf("unknown actor %q (want orchestrator, task, or narrator)", name)
		}
		if p.Model == "" {
			if p.Provider != "" {
				return cfg, fmt.Errorf("%s: pick a model at %s", name, p.Provider)
			}
			if p.Effort != "" {
				a.ReasoningEffort = p.Effort
			}
			continue
		}
		picked, err := config.PickRoute(project, *a, p.Provider, p.Model, p.Effort)
		if err != nil {
			return cfg, fmt.Errorf("%s: %w", name, err)
		}
		*a = picked
	}
	return cfg, cfg.Validate()
}

// startPicks reads the configuration and model picks a session.start
// carries and checks them against the configuration they apply to, so a
// bad pick is refused before anything starts.
func startPicks(project string, params map[string]any) (string, map[string]RoutePick, error) {
	text := func(key string) string { v, _ := params[key].(string); return strings.TrimSpace(v) }
	name := text("config")
	picks := map[string]RoutePick{}
	for _, actor := range settings.StartActors {
		p := RoutePick{Provider: text(actor + "_provider"), Model: text(actor + "_model"), Effort: text(actor + "_effort")}
		if p != (RoutePick{}) {
			picks[actor] = p
		}
	}
	preset, bundle := SplitConfig(name)
	cfg, err := config.LoadBundle(project, preset, bundle)
	if err != nil {
		return "", nil, err
	}
	if _, err := ApplyPicks(project, cfg, picks); err != nil {
		return "", nil, err
	}
	return name, picks, nil
}

// SessionStarter starts a new session in the project and returns its id once
// the session exists on disk.
type SessionStarter func(ctx context.Context, project string, req SessionRequest) (SessionStart, error)

// AnnounceEnv names a file a freshly started eagent process writes its
// session id to, so the process that spawned it can report the id.
const AnnounceEnv = "EAGENT_ANNOUNCE_SESSION"

var (
	starterMu sync.Mutex
	starter   SessionStarter = spawnDetachedSession
)

// SetSessionStarter makes phone-initiated sessions run through fn (an eagent
// server hosts them in-process). It returns a function that restores the
// previous starter.
func SetSessionStarter(fn SessionStarter) func() {
	starterMu.Lock()
	defer starterMu.Unlock()
	previous := starter
	starter = fn
	return func() {
		starterMu.Lock()
		defer starterMu.Unlock()
		starter = previous
	}
}

func startSession(ctx context.Context, project string, req SessionRequest) (SessionStart, error) {
	starterMu.Lock()
	fn := starter
	starterMu.Unlock()
	return fn(ctx, project, req)
}

// spawnCommand builds the detached eagent process; tests substitute it.
var spawnCommand = func(project string, req SessionRequest, announce string) (*exec.Cmd, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	args := []string{"-C", project, "-p"}
	if req.Dir != "" {
		args = append(args, "--cwd", req.Dir)
	}
	if req.Relay != "" {
		args = append(args, "--relay", req.Relay)
	}
	preset, bundle := SplitConfig(req.Config)
	if preset != "" {
		args = append(args, "--preset", preset)
	}
	if bundle != "" {
		args = append(args, "--config", bundle)
	}
	env := append(os.Environ(), AnnounceEnv+"="+announce)
	if len(req.Routes) > 0 {
		// The picks reach the new process as its actor overrides.
		cfg, err := config.LoadBundle(project, preset, bundle)
		if err != nil {
			return nil, err
		}
		picked, err := ApplyPicks(project, cfg, req.Routes)
		if err != nil {
			return nil, err
		}
		for name, a := range map[string]config.Actor{"ORCHESTRATOR": picked.Orchestrator, "TASK": picked.Task, "NARRATOR": picked.Narrator} {
			env = append(env, "EAGENT_"+name+"_MODEL="+a.Model, "EAGENT_"+name+"_BASE_URL="+a.BaseURL, "EAGENT_"+name+"_API_KEY_ENV="+a.APIKeyEnv, "EAGENT_"+name+"_PROTOCOL="+a.Protocol, "EAGENT_"+name+"_REASONING_EFFORT="+a.ReasoningEffort)
		}
	}
	cmd := exec.Command(exe, append(args, req.Prompt)...)
	cmd.Env = env
	return cmd, nil
}

// spawnDetachedSession is the starter when no long-lived host registered
// one: a terminal session that happens to hold the project's connector. The
// new session gets a process of its own (its own session group, so it
// survives this process ending) and runs as a batch session with the phone
// mirror on; it reports its id through AnnounceEnv.
func spawnDetachedSession(ctx context.Context, project string, req SessionRequest) (SessionStart, error) {
	dir := filepath.Join(stateDir(project), "spawned")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return SessionStart{}, err
	}
	id := randomID()
	announce := filepath.Join(dir, id+".session")
	logPath := filepath.Join(dir, id+".log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return SessionStart{}, err
	}
	cmd, err := spawnCommand(project, req, announce)
	if err != nil {
		logFile.Close()
		return SessionStart{}, err
	}
	cmd.Dir = project
	cmd.Stdout, cmd.Stderr = logFile, logFile
	detachProcess(cmd)
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return SessionStart{}, fmt.Errorf("start eagent: %w", err)
	}
	logFile.Close()
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	deadline := time.After(20 * time.Second)
	for {
		if raw, err := os.ReadFile(announce); err == nil && strings.TrimSpace(string(raw)) != "" {
			_ = os.Remove(announce)
			return SessionStart{ID: strings.TrimSpace(string(raw)), Host: "detached", Interactive: false,
				Message: "Started as a batch session in its own process; it runs until the work is done. Continue it later from eagent's web UI or with eagent -c."}, nil
		}
		select {
		case err := <-exited:
			return SessionStart{}, fmt.Errorf("eagent exited before starting a session (%v): %s", err, logTail(logPath))
		case <-ctx.Done():
			return SessionStart{}, ctx.Err()
		case <-deadline:
			return SessionStart{}, errors.New("the new eagent process did not report a session within 20 seconds; see " + logPath)
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func logTail(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 {
		return "(no output)"
	}
	text := strings.TrimSpace(string(raw))
	if len(text) > 600 {
		text = "…" + text[len(text)-600:]
	}
	return text
}

// checkStartDir accepts an empty directory (the project root) or an absolute
// path to a directory a shell can start in.
func checkStartDir(dir string) error {
	if dir == "" {
		return nil
	}
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("the start directory must be an absolute path (or ~/…)")
	}
	if err := procs.CheckDir(dir); err != nil {
		return fmt.Errorf("the start directory: %w", err)
	}
	return nil
}

// relayProfiles describes the agents a session started from the phone may
// relay to, for the app's agent picker: name, program, and a label.
func relayProfiles(project string) []map[string]any {
	cfg, err := config.Load(project, "")
	if err != nil {
		cfg = config.Defaults()
	}
	out := []map[string]any{}
	profiles := cfg.Relay.RelayProfiles()
	for _, name := range cfg.Relay.RelayProfileNames() {
		p := profiles[name]
		out = append(out, map[string]any{"name": name, "agent": p.Agent, "label": p.Label, "model": p.Model, "effort": p.Effort})
	}
	return out
}

// runningRelays lists the relays of this project's running sessions, for the
// app's "running now" list. A relay's terminal can outlive its session by
// days (the agent sits idle at its prompt); those are not "running now", so
// only relays whose eagent session is alive are listed. Only stable fields,
// so the published snapshot changes only when a relay starts, stops or exits.
func runningRelays(live []string) []map[string]any {
	out := []map[string]any{}
	alive := map[string]bool{}
	for _, sid := range live {
		alive[sid] = true
	}
	if len(alive) == 0 {
		return out
	}
	for _, h := range []relay.Host{relay.Tmux{}, relay.Screen{}} {
		if _, err := exec.LookPath(h.Name()); err != nil {
			continue
		}
		listed, _ := h.List()
		for _, l := range listed {
			session := relay.SessionOf(l.Name)
			if !alive[session] {
				continue
			}
			name := strings.TrimPrefix(strings.TrimPrefix(l.Name, relay.Prefix), session+"-")
			agent := name
			for _, a := range []string{"codex", "claude"} {
				if strings.HasPrefix(name, a) {
					agent = a
				}
			}
			state := "running"
			if l.Dead {
				state = "exited"
			}
			item := map[string]any{"name": name, "target": l.Name, "session": session, "thread": "ext:eagent:" + session, "dir": l.Dir, "agent": agent, "state": state}
			if !l.Created.IsZero() {
				item["created"] = l.Created.UTC().Format(time.RFC3339)
			}
			out = append(out, item)
		}
	}
	return out
}

// projectDirectories lists where a new session may start: the project root,
// directories recent sessions moved to, the project's own subdirectories, and
// its siblings. Names starting with a dot and dependency trees are skipped.
func projectDirectories(project string) map[string]any {
	root, err := filepath.Abs(project)
	if err != nil {
		root = project
	}
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	list := func(dir string, skip string) []string {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return []string{}
		}
		out := []string{}
		for _, e := range entries {
			name := e.Name()
			if !e.IsDir() || strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" || name == "target" || name == "__pycache__" {
				continue
			}
			full := filepath.Join(dir, name)
			if full == skip {
				continue
			}
			out = append(out, full)
			if len(out) >= 40 {
				break
			}
		}
		sort.Strings(out)
		return out
	}
	recent := []string{}
	seen := map[string]bool{root: true}
	if infos, err := store.List(store.Root(project)); err == nil {
		if len(infos) > 10 {
			infos = infos[len(infos)-10:]
		}
		for i := len(infos) - 1; i >= 0 && len(recent) < 10; i-- {
			for _, dir := range recentDirs(infos[i].Path) {
				if seen[dir] {
					continue
				}
				if st, err := os.Stat(dir); err == nil && st.IsDir() {
					seen[dir] = true
					recent = append(recent, dir)
				}
			}
		}
	}
	return map[string]any{"root": root, "recent": recent, "children": list(root, ""), "siblings": list(filepath.Dir(root), root)}
}

// recentDirs returns the directories a session moved to, newest first,
// reading only the lines that can hold a move.
func recentDirs(sessionPath string) []string {
	entries, err := os.ReadDir(sessionPath)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(sessionPath, e.Name()))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(raw), "\n") {
			if !strings.Contains(line, `"cwd.change"`) {
				continue
			}
			var ev struct {
				Type  string `json:"type"`
				Actor string `json:"actor"`
				Task  string `json:"task"`
				Data  struct {
					Path string `json:"path"`
				} `json:"data"`
			}
			if json.Unmarshal([]byte(line), &ev) == nil && ev.Type == "cwd.change" && ev.Task == "" && ev.Data.Path != "" {
				out = append(out, ev.Data.Path)
			}
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}
