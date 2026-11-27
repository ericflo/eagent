// Package relay runs an external coding agent (Codex, Claude Code) in a
// terminal session eagent owns and turns what the agent does into events.
//
// The terminal is only used to type and to read dialogs. What the agent says
// and does is read from its own transcript (Codex rollouts, Claude Code
// session files), which is exact, complete, and never scrolls off.
package relay

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Host is a terminal multiplexer that keeps agents running in named
// sessions, independent of the eagent process that started them.
type Host interface {
	Name() string
	// Start launches argv in dir under the session name, detached.
	Start(name, dir string, argv, env []string) error
	// Alive reports whether the session exists and its program still runs.
	Alive(name string) bool
	// PID is the agent process's pid, 0 when unknown.
	PID(name string) int
	// Paste types text exactly as a bracketed paste (multi-line safe), without
	// pressing Enter.
	Paste(name, text string) error
	// Keys presses named keys: Enter, Escape, Up, Down, Tab, C-c.
	Keys(name string, keys ...string) error
	// Capture returns the visible screen (lines 0) or the screen plus up to
	// lines of scrollback, as text.
	Capture(name string, lines int) (string, error)
	// Kill ends the session and its program.
	Kill(name string) error
	// List names the sessions eagent started on this machine.
	List() ([]Listed, error)
	// Attach is the command a person runs to watch or type into the session.
	Attach(name string) []string
}

// Listed is one running relay session.
type Listed struct {
	Name    string
	Dir     string
	Created time.Time
	Dead    bool
}

// Prefix starts every session name eagent creates, so it never touches the
// user's own terminal sessions (including ones named "eagent-…" by hand).
const Prefix = "eagent-relay-"

// Socket is the private tmux socket eagent's relays run on (a variable so
// tests use their own).
var Socket = "eagent"

// Detect returns the host for a profile's preference: tmux when installed
// (or asked for), otherwise screen.
func Detect(preference string) (Host, error) {
	switch preference {
	case "tmux":
		if _, err := exec.LookPath("tmux"); err != nil {
			return nil, errors.New("tmux is not installed")
		}
		return Tmux{}, nil
	case "screen":
		if _, err := exec.LookPath("screen"); err != nil {
			return nil, errors.New("screen is not installed")
		}
		return Screen{}, nil
	case "":
		if _, err := exec.LookPath("tmux"); err == nil {
			return Tmux{}, nil
		}
		if _, err := exec.LookPath("screen"); err == nil {
			return Screen{}, nil
		}
		return nil, errors.New("a relay needs tmux (preferred) or screen; install one")
	}
	return nil, fmt.Errorf("unknown relay host %q (tmux or screen)", preference)
}

// HostByName returns the host a relay was started on, for reattaching.
func HostByName(name string) Host {
	if name == "screen" {
		return Screen{}
	}
	return Tmux{}
}

// ---- tmux -----------------------------------------------------------------

// Tmux hosts relays on a private socket (tmux -L eagent).
type Tmux struct{}

func (Tmux) Name() string { return "tmux" }

// bounded makes a short-lived helper command (tmux, screen, git, an agent
// CLI) that cannot hang its caller: it is killed after timeout, and its
// output pipes are abandoned soon after even if a child keeps them open.
// A wedged tmux server or a git credential prompt otherwise wedges the
// session that called it.
func bounded(timeout time.Duration, name string, args ...string) (*exec.Cmd, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = pipeWait
	return cmd, cancel
}

// pipeWait is how long a helper command's output is waited for once the
// command itself has exited. A variable so tests can shorten it.
var pipeWait = 2 * time.Second

// helperTimeout bounds one tmux or screen command.
const helperTimeout = 15 * time.Second

func tmux(stdin []byte, args ...string) (string, error) {
	cmd, cancel := bounded(helperTimeout, "tmux", append([]string{"-L", Socket}, args...)...)
	defer cancel()
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("tmux %s: %v: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// tmuxArg keeps one argument an argument. tmux splits its own command line
// at any argument that ends in ";" (a lone ";" included) and runs what
// follows as another tmux command, such as run-shell, so a program's
// arguments, a folder or a key could otherwise run a shell command. tmux
// reads a trailing "\;" as a literal ";" and leaves every other character
// alone.
func tmuxArg(s string) string {
	if strings.HasSuffix(s, ";") {
		return s[:len(s)-1] + `\;`
	}
	return s
}

// tmuxSession and tmuxPane name exactly one session: without "=", tmux
// takes a name no session has as the start of another's name, so a relay
// that has ended would be read, typed into or killed through a sibling
// whose name starts with its own ("…-pi" and "…-pi-2").
func tmuxSession(name string) string { return "=" + name }
func tmuxPane(name string) string    { return "=" + name + ":" }

func (Tmux) Start(name, dir string, argv, env []string) error {
	// The scrollback limit only applies to panes created after it is set, so
	// it goes first, on eagent's private server.
	args := []string{"start-server", ";", "set-option", "-g", "history-limit", "50000", ";",
		"new-session", "-d", "-s", tmuxArg(name), "-x", "200", "-y", "50", "-c", tmuxArg(dir)}
	for _, kv := range env {
		args = append(args, "-e", tmuxArg(kv))
	}
	args = append(args, "--")
	for _, a := range argv {
		args = append(args, tmuxArg(a))
	}
	// Keep the pane when the program exits, so its last words can be read
	// and the exit noticed rather than the session silently vanishing.
	args = append(args, ";", "set-option", "-t", tmuxPane(name), "remain-on-exit", "on")
	_, err := tmux(nil, args...)
	return err
}

func (Tmux) Alive(name string) bool {
	out, err := tmux(nil, "display-message", "-p", "-t", tmuxPane(name), "#{pane_dead}")
	return err == nil && strings.TrimSpace(out) == "0"
}

func (Tmux) PID(name string) int {
	out, err := tmux(nil, "display-message", "-p", "-t", tmuxPane(name), "#{pane_pid}")
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(out))
	return n
}

func (Tmux) Paste(name, text string) error {
	buf := "eagent-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if _, err := tmux([]byte(text), "load-buffer", "-b", buf, "-"); err != nil {
		return err
	}
	_, err := tmux(nil, "paste-buffer", "-p", "-d", "-b", buf, "-t", tmuxPane(name))
	return err
}

func (Tmux) Keys(name string, keys ...string) error {
	for _, k := range keys {
		// "--" so a key that starts with "-" is typed, not read as a flag.
		if _, err := tmux(nil, "send-keys", "-t", tmuxPane(name), "--", tmuxArg(k)); err != nil {
			return err
		}
	}
	return nil
}

func (Tmux) Capture(name string, lines int) (string, error) {
	if lines <= 0 {
		return tmux(nil, "capture-pane", "-p", "-J", "-t", tmuxPane(name))
	}
	return tmux(nil, "capture-pane", "-p", "-J", "-t", tmuxPane(name), "-S", "-"+strconv.Itoa(lines))
}

func (Tmux) Kill(name string) error {
	_, err := tmux(nil, "kill-session", "-t", tmuxSession(name))
	return err
}

func (Tmux) List() ([]Listed, error) {
	out, err := tmux(nil, "list-sessions", "-F", "#{session_name}\t#{session_created}\t#{pane_current_path}\t#{pane_dead}")
	if err != nil {
		if strings.Contains(out, "no server running") || strings.Contains(out, "error connecting") || strings.Contains(out, "No such file") {
			return nil, nil
		}
		return nil, err
	}
	var listed []Listed
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Split(line, "\t")
		if len(f) < 4 || !strings.HasPrefix(f[0], Prefix) {
			continue
		}
		secs, _ := strconv.ParseInt(f[1], 10, 64)
		listed = append(listed, Listed{Name: f[0], Created: time.Unix(secs, 0), Dir: f[2], Dead: f[3] == "1"})
	}
	return listed, nil
}

// Attach names the session exactly, like every other tmux call, so an
// ended relay's command never opens a sibling whose name starts with its
// own. The target is joined to -t: a person pastes this line into a shell,
// and zsh replaces a word that starts with "=" by a command's path.
func (Tmux) Attach(name string) []string {
	return []string{"tmux", "-L", Socket, "attach-session", "-t" + tmuxSession(name)}
}

// ---- screen -----------------------------------------------------------------

// Screen hosts relays in GNU screen sessions named with Prefix. It is the
// fallback where tmux is not installed.
type Screen struct{}

func (Screen) Name() string { return "screen" }

// screen sends a command to the named session only. screen -S takes a name
// as the start of any session's, so it is given the live session's
// "pid.name", which no other session can start with.
func screen(name string, args ...string) (string, error) {
	pid := Screen{}.PID(name)
	if pid <= 0 {
		return "", fmt.Errorf("screen %s: no session named %s", args[0], name)
	}
	cmd, cancel := bounded(helperTimeout, "screen", append([]string{"-S", strconv.Itoa(pid) + "." + name, "-p", "0", "-X"}, args...)...)
	defer cancel()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("screen %s: %v: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func (Screen) Start(name, dir string, argv, env []string) error {
	cmd, cancel := bounded(helperTimeout, "screen", append([]string{"-dmS", name, "-h", "50000"}, argv...)...)
	defer cancel()
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("screen: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (s Screen) Alive(name string) bool { return s.PID(name) > 0 }

// PID finds the screen session's pid from `screen -ls` (the session's own
// pid; its child is the agent). A session screen lists as dead (its process
// gone, its socket left behind) has none.
func (Screen) PID(name string) int {
	cmd, cancel := bounded(helperTimeout, "screen", "-ls", name)
	defer cancel()
	out, _ := cmd.CombinedOutput()
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		dot := strings.IndexByte(line, '.')
		if dot <= 0 {
			continue
		}
		rest := line[dot+1:]
		if tab := strings.IndexAny(rest, " \t"); tab >= 0 {
			rest = rest[:tab]
		}
		if rest != name || strings.Contains(strings.ToLower(line[dot+1+len(rest):]), "dead") {
			continue
		}
		n, _ := strconv.Atoi(line[:dot])
		return n
	}
	return 0
}

// Paste loads the text into screen's paste buffer from a file and pastes
// it wrapped in bracketed-paste markers, so newlines do not submit early.
func (Screen) Paste(name, text string) error {
	f, err := os.CreateTemp("", "eagent-paste-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString("\x1b[200~" + text + "\x1b[201~"); err != nil {
		f.Close()
		return err
	}
	f.Close()
	if _, err := screen(name, "readbuf", f.Name()); err != nil {
		return err
	}
	_, err = screen(name, "paste", ".")
	return err
}

var screenKeys = map[string]string{"Enter": "\r", "Escape": "\x1b", "Up": "\x1b[A", "Down": "\x1b[B", "Tab": "\t", "C-c": "\x03", "Space": " "}

func (Screen) Keys(name string, keys ...string) error {
	for _, k := range keys {
		seq, ok := screenKeys[k]
		if !ok {
			seq = k
		}
		if _, err := screen(name, "stuff", seq); err != nil {
			return err
		}
	}
	return nil
}

func (Screen) Capture(name string, lines int) (string, error) {
	f, err := os.CreateTemp("", "eagent-hardcopy-*")
	if err != nil {
		return "", err
	}
	path := f.Name()
	f.Close()
	defer os.Remove(path)
	args := []string{"hardcopy", path}
	if lines > 0 {
		args = []string{"hardcopy", "-h", path}
	}
	if _, err := screen(name, args...); err != nil {
		return "", err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	all := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if lines > 0 && len(all) > lines {
		all = all[len(all)-lines:]
	}
	return strings.Join(all, "\n"), nil
}

func (Screen) Kill(name string) error {
	_, err := screen(name, "quit")
	return err
}

func (Screen) List() ([]Listed, error) {
	cmd, cancel := bounded(helperTimeout, "screen", "-ls")
	defer cancel()
	out, _ := cmd.CombinedOutput()
	var listed []Listed
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		dot := strings.IndexByte(line, '.')
		if dot <= 0 {
			continue
		}
		rest := line[dot+1:]
		if tab := strings.IndexAny(rest, " \t"); tab >= 0 {
			rest = rest[:tab]
		}
		if strings.HasPrefix(rest, Prefix) {
			listed = append(listed, Listed{Name: rest})
		}
	}
	return listed, nil
}

// Attach names the live session as pid.name, as the other screen calls do:
// screen -r takes a bare name as the start of any session's. When the
// session is gone the command says so rather than attach to a sibling.
func (Screen) Attach(name string) []string {
	if pid := (Screen{}).PID(name); pid > 0 {
		return []string{"screen", "-r", strconv.Itoa(pid) + "." + name}
	}
	return []string{"sh", "-c", `echo "screen session $1 is not running" >&2; exit 1`, "sh", name}
}

// Target is the terminal session name for a relay: the eagent session id
// and the relay's name, so two sessions' relays never collide.
func Target(session, name string) string {
	clean := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, name)
	return Prefix + session + "-" + clean
}

// SessionOf extracts the eagent session id from a target name.
func SessionOf(target string) string {
	rest := strings.TrimPrefix(target, Prefix)
	if i := strings.IndexByte(rest, '-'); i > 0 {
		return rest[:i]
	}
	return ""
}

// home is the user's home directory, overridable for tests.
func home() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return filepath.Clean("~")
}

// ExpandHome reads a folder the way a person writes it: "~" and "~/x" are
// in the home directory. Anything else comes back as it was.
func ExpandHome(dir string) string {
	if dir != "~" && !strings.HasPrefix(dir, "~/") {
		return dir
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return dir
	}
	return filepath.Join(h, dir[1:])
}

// KillSocket ends the tmux server on a socket eagent (or a test) owns and
// removes the socket file, which tmux can leave behind.
func KillSocket(socket string) {
	cmd, cancel := bounded(helperTimeout, "tmux", "-L", socket, "kill-server")
	defer cancel()
	_ = cmd.Run()
	dir := os.Getenv("TMUX_TMPDIR")
	if dir == "" {
		dir = "/tmp"
	}
	_ = os.Remove(filepath.Join(dir, fmt.Sprintf("tmux-%d", os.Getuid()), socket))
}
