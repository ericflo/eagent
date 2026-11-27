// Command eagent runs a coding agent (Claude Code, Codex, Antigravity or pi)
// in a terminal it owns and stands between that agent and the user.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/finalechat"
	"github.com/ericflo/eagent/internal/harness"
	"github.com/ericflo/eagent/internal/integration"
	"github.com/ericflo/eagent/internal/llm"
	"github.com/ericflo/eagent/internal/prompts"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/settings"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/store"
	"github.com/ericflo/eagent/internal/tools"
	"github.com/ericflo/eagent/internal/ui"
	"github.com/ericflo/eagent/internal/web"
)

// version is stamped by the Makefile and the release workflow; a plain
// `go install` build falls back to the module version Go recorded.
var version = "dev"

const usage = `eagent — runs a coding agent in a terminal and stands between it and you

Usage:
  eagent [--addr HOST:PORT]          serve this directory's web UI and phone host (as eagent serve)
  eagent [flags] "prompt"            start a session: the agent gets your message word for word
  eagent -p [flags] "prompt"         batch mode: run until the work is done, then exit
  eagent -c [flags] [prompt]         continue the most recent session in this project
  eagent relay <profile> [--cwd DIR] [prompt]
                                     the same, with this agent profile (as --relay)
  eagent relays [attach|stop NAME]   list the relays running on this machine, watch one, or stop one
  eagent resume [flags] <id> [prompt]
  eagent sessions                    list sessions in this project
  eagent summarize <id>... | --all [--dry-run] [--json] [-v]
                                     write a fresh title and summary for stored sessions
                                     to their FinaleChat threads (running sessions do this
                                     themselves; this is for ones from before they did)
  eagent show <id> [--raw] [--actor eagent|user|harness] [--task tN (older sessions)]
  eagent replay <id>                 rebuild state from the log and print it
  eagent artifact export <id> --output DIR
                                     export a portable website and native source logs
  eagent artifact publish <id> [--recreate]
                                    publish; recreate only a previously deleted archive
  eagent artifact verify DIR         verify a downloaded archive without executing it
  eagent artifact restore DIR --output NEW_DIR
                                    recover native sources into a new directory
  eagent artifact enable|disable     enable/disable proactive archives (on by default)
  eagent connector pair             request remote settings access in FinaleChat
  eagent connector run              keep remote settings available without a session
  eagent serve [--addr 127.0.0.1:7331]  web UI: live chat, session browser, the agent's screen, config
  eagent doctor [--live]             check configuration and credentials
  eagent config                      print the effective configuration
  eagent config list                 built-in presets and the project's saved presets
  eagent config save NAME [desc]     save the effective configuration as a named preset
  eagent config show NAME            print a preset's effective configuration
  eagent config use NAME|default     set (or clear) the project's default preset
  eagent config delete NAME          delete a saved preset
  eagent presets [list|save|show|use|delete]
                                     manage built-in and saved presets (shorthand for config)
  eagent servers                     list registered local/custom model servers
  eagent servers add URL [flags]     probe and register a local/custom model server
  eagent servers probe URL [flags]   probe a server's /models and API kinds without saving
  eagent servers refresh [ID]        re-probe one or all registered servers
  eagent servers remove ID           unregister a server
  eagent servers use ID [MODEL]      point eagent's model at a registered server
  eagent prompts [export|show NAME]  list, export, or print eagent's prompts
  eagent view <id> [--until SEQ]     print the exact prompt eagent's model would receive (for prompt work)
  eagent version

Flags (before positional arguments):
  -p            non-interactive: exits when the work is done and its agent stopped
                (code 0), or when it needs an answer (code 2; the agent keeps running)
  -c            resume the latest session
  -v            verbose: show every tool call and model response
  --json        emit what eagent says as JSON lines on stdout
  -C <dir>      project directory (default: current directory)
  --preset <n>  built-in or saved preset name (or EAGENT_PRESET); built-ins:
                glm (default), deepseek, qwen, mistral, muse, muse-open,
                beam, deepseek-1017, or a provider tier: openai-high|low,
                openrouter-high|med|low, anthropic-high|med, deepinfra-high|med|low,
                fireworks-high|med|low, opencode-high|med|low, nous-high|med|low
  --config <n>  saved preset / bundle from .agents/eagent/configs/ (or EAGENT_CONFIG)
  --answer <s>  answer the pending question when resuming
  --cwd <dir>   start a new session's agent in this directory (default: the project)
  --relay <p>   the agent profile a new session starts (antigravity, claude, codex, pi,
                or one in config; default: relay.default, else the first installed)
  --relay-model <m>, --relay-effort <e>
                the agent's model and reasoning effort (default: the
                profile's, else the agent's own configuration)
  --serve <a>   also serve the web UI at this address while a session runs (e.g. 127.0.0.1:7331)

Credentials come from the environment: TOGETHER_API_KEY (default models),
OPENAI_API_KEY / OPENROUTER_API_KEY (openai-* presets, OpenRouter is the
fallback), OPENROUTER_API_KEY (openrouter-*, qwen, mistral, muse-open, beam,
deepseek-1017), ANTHROPIC_API_KEY (anthropic-*),
DEEPINFRA_API_KEY, FIREWORKS_API_KEY, OPENCODE_ZEN_API_KEY, NOUS_API_KEY.
Sessions are stored in <project>/.agents/eagent/sessions/.
`

func main() {
	if version == "dev" {
		if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
			version = strings.TrimPrefix(info.Main.Version, "v")
		}
	}
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	fs := flag.NewFlagSet("eagent", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	var (
		batch     = fs.Bool("p", false, "non-interactive")
		cont      = fs.Bool("c", false, "continue latest session")
		verbose   = fs.Bool("v", false, "verbose")
		jsonOut   = fs.Bool("json", false, "json output")
		dir       = fs.String("C", "", "project directory")
		preset    = fs.String("preset", "", "model preset")
		bundle    = fs.String("config", "", "named config bundle")
		addr      = fs.String("addr", "127.0.0.1:7331", "serve: listen address")
		serveAt   = fs.String("serve", "", "serve the web UI while running")
		answer    = fs.String("answer", "", "answer to the pending question")
		startDir  = fs.String("cwd", "", "start a new session's agent in this directory instead of the project root")
		relayTo   = fs.String("relay", "", "the agent profile a new session starts (default: relay.default, else the first installed)")
		relayMdl  = fs.String("relay-model", "", "the agent's model (default: the profile's, else the agent's own)")
		relayEff  = fs.String("relay-effort", "", "the agent's reasoning effort (default: the profile's, else the agent's own)")
		showRaw   = fs.Bool("raw", false, "show raw events")
		actor     = fs.String("actor", "", "filter by actor")
		task      = fs.String("task", "", "filter by task id")
		until     = fs.Int64("until", 0, "view: only events up to this seq")
		asJSON    = fs.Bool("as-json", false, "view: machine-readable output")
		live      = fs.Bool("live", false, "doctor: make a small live call per model")
		write     = fs.Bool("write", false, "config: write config.json")
		output    = fs.String("output", "", "artifact export/restore: new destination directory")
		recreate  = fs.Bool("recreate", false, "artifact publish: explicitly recreate a deleted archive")
		allFlag   = fs.Bool("all", false, "summarize: every session in the project")
		dryRun    = fs.Bool("dry-run", false, "summarize: print the new title and summary without sending them")
		srvID     = fs.String("id", "", "servers: custom server id")
		srvLabel  = fs.String("label", "", "servers: custom server display label")
		srvProto  = fs.String("protocol", "", "servers: wire protocol (openai-chat, openai-responses, anthropic)")
		srvKeyEnv = fs.String("key-env", "", "servers: optional API key environment variable name")
		srvNote   = fs.String("note", "", "servers: optional note")
		srvEffort = fs.String("effort", "", "servers use: reasoning effort")
		noProbe   = fs.Bool("no-probe", false, "servers add: register without probing first")
		helpFlag  = fs.Bool("h", false, "help")
	)
	fs.BoolVar(helpFlag, "help", false, "help")
	// Arguments after "--" are the prompt's words, never a command.
	rest, commands, err := parseArgs(fs, args)
	if err != nil {
		return 64 // EX_USAGE
	}
	if *helpFlag {
		fmt.Fprint(os.Stdout, usage)
		return 0
	}
	harness.Version = version
	if commands > 0 && len(rest) == 2 && rest[0] == "relay-agy-status" {
		return relay.WriteAntigravityStatus(rest[1], os.Stdin, os.Stdout)
	}
	if commands > 0 && rest[0] == "relay-hook" {
		// Claude Code's Stop hook for a relay: fast, and silent unless it
		// has messages to hand over.
		if len(rest) < 2 {
			return 64
		}
		return relay.RunStopHook(rest[1], os.Stdin, os.Stdout)
	}

	project, err := resolveProject(*dir)
	if err != nil {
		return fail(err)
	}

	cmd := ""
	if commands > 0 {
		switch rest[0] {
		case "sessions", "show", "replay", "resume", "doctor", "config", "presets", "preset", "servers", "prompts", "serve", "view", "version", "help", "artifact", "connector", "relay", "relays", "summarize":
			cmd = rest[0]
			rest = rest[1:]
		}
	}
	switch cmd {
	case "help":
		fmt.Fprint(os.Stdout, usage)
		return 0
	case "version":
		fmt.Println("eagent", version)
		return 0
	case "sessions":
		return cmdSessions(project)
	case "summarize":
		return cmdSummarize(project, *preset, *bundle, rest, *allFlag, *dryRun, *jsonOut, *verbose)
	case "show":
		if len(rest) < 1 {
			return fail(errors.New("usage: eagent show <session-id>"))
		}
		return cmdShow(project, rest[0], *showRaw, *actor, *task)
	case "replay":
		if len(rest) < 1 {
			return fail(errors.New("usage: eagent replay <session-id>"))
		}
		return cmdReplay(project, rest[0])
	case "doctor":
		return cmdDoctor(project, *preset, *bundle, *live)
	case "config":
		return cmdConfig(project, *preset, *bundle, *write, rest)
	case "presets", "preset":
		if len(rest) == 0 {
			rest = []string{"list"}
		}
		return cmdConfig(project, *preset, *bundle, *write, rest)
	case "servers":
		return cmdServers(project, rest, serverCLIOpts{
			id: *srvID, label: *srvLabel, protocol: *srvProto, keyEnv: *srvKeyEnv,
			note: *srvNote, effort: *srvEffort, noProbe: *noProbe, asJSON: *jsonOut,
		})
	case "prompts":
		return cmdPrompts(project, rest)
	case "serve":
		return cmdServe(project, *addr, *preset, *bundle, *verbose)
	case "artifact":
		return cmdArtifact(project, rest, *output, *recreate)
	case "connector":
		return cmdConnector(project, rest)
	case "relays":
		return cmdRelays(project, rest)
	case "view":
		if len(rest) < 1 {
			return fail(errors.New("usage: eagent view <session-id> [--until SEQ]"))
		}
		viewIgnoredFlags(os.Stderr, *actor, *task)
		return cmdView(project, rest[0], *until, *asJSON)
	}

	if cmd == "" && len(rest) == 0 && !*batch && !*cont && *answer == "" && strings.TrimSpace(*relayTo) == "" && *startDir == "" && *serveAt == "" && stdinIsTerminal() {
		// Plain `eagent` in a project: its web UI and phone host.
		return cmdServe(project, *addr, *preset, *bundle, *verbose)
	}
	cfg, err := config.LoadBundle(project, *preset, *bundle)
	if err != nil {
		return fail(err)
	}
	// Interactive only when someone can actually type: a pipe, a file, or
	// /dev/null on stdin would otherwise read as an immediate "quit" and the
	// run would exit 0 having done nothing.
	opts := harness.Options{Project: project, Interactive: !*batch && stdinIsTerminal(), Verbose: *verbose, Answer: *answer, Relay: strings.TrimSpace(*relayTo), RelayModel: strings.TrimSpace(*relayMdl), RelayEffort: strings.TrimSpace(*relayEff)}
	if cmd == "relay" {
		names := cfg.Relay.RelayProfileNames()
		if len(rest) < 1 {
			return fail(fmt.Errorf("usage: eagent relay <profile> [--cwd DIR] [prompt]  (profiles: %s)", strings.Join(names, ", ")))
		}
		opts.Relay, rest = rest[0], rest[1:]
	}
	if opts.Relay != "" {
		if _, ok := cfg.Relay.RelayProfiles()[opts.Relay]; !ok {
			return fail(fmt.Errorf("no agent profile %q (have %s)", opts.Relay, strings.Join(cfg.Relay.RelayProfileNames(), ", ")))
		}
	}
	if *startDir != "" {
		dir, err := filepath.Abs(relay.ExpandHome(*startDir))
		if err != nil {
			return fail(err)
		}
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			return fail(fmt.Errorf("--cwd %s is not a directory", *startDir))
		}
		opts.StartDir = dir
	}
	var sessionPath string
	if cmd == "resume" {
		if len(rest) < 1 {
			return fail(errors.New("usage: eagent resume <session-id> [prompt]"))
		}
		info, err := store.Resolve(store.Root(project), rest[0])
		if err != nil {
			return fail(err)
		}
		sessionPath = info.Path
		rest = rest[1:]
	} else if *cont {
		info, err := store.Resolve(store.Root(project), "latest")
		if err != nil {
			return fail(err)
		}
		sessionPath = info.Path
	}
	opts.Prompt = strings.TrimSpace(strings.Join(rest, " "))
	if opts.Prompt == "" && !opts.Interactive && sessionPath == "" {
		// A non-interactive run may take its prompt on stdin.
		if fi, err := os.Stdin.Stat(); err == nil && fi.Mode()&os.ModeCharDevice == 0 {
			raw, _ := readAll(os.Stdin)
			opts.Prompt = strings.TrimSpace(string(raw))
		}
		if batchNeedsPrompt(opts.Prompt, opts.Relay) {
			return fail(errors.New("a non-interactive run needs a prompt: eagent -p \"...\" (stdin is not a terminal)"))
		}
	}

	term := ui.New(ui.Options{Interactive: opts.Interactive, Verbose: *verbose, JSON: *jsonOut})
	var rt *harness.Runtime
	if sessionPath != "" {
		rt, err = harness.Resume(cfg, opts, term, sessionPath)
	} else {
		rt, err = harness.New(cfg, opts, term)
	}
	if err != nil {
		term.Close("")
		return fail(err)
	}
	if announce := os.Getenv(integration.AnnounceEnv); announce != "" {
		// Started on behalf of a phone request: tell the spawning process
		// which session this is, then keep the variable out of tool shells.
		if sessionPath == "" {
			if err := os.WriteFile(announce+".tmp", []byte(rt.SessionID()+"\n"), 0o644); err == nil {
				_ = os.Rename(announce+".tmp", announce)
			}
		}
		_ = os.Unsetenv(integration.AnnounceEnv)
	}
	term.Banner(rt.SessionID(), cfg.Model.Model, sessionPath != "")
	if opts.Interactive && opts.Prompt == "" && sessionPath == "" {
		term.Log("type what you want done, or /help")
	}

	ctx, cancel := context.WithCancel(context.Background())
	if *serveAt != "" {
		ws := web.New(project, term.Log)
		ws.Preset, ws.Bundle, ws.Verbose = *preset, *bundle, *verbose
		go func() {
			if err := web.ListenAndServe(ctx, *serveAt, ws); err != nil && !errors.Is(err, http.ErrServerClosed) {
				term.Log("web: %v", err)
			}
		}()
		term.Log("web UI: http://%s/#/s/%s/chat", displayAddr(*serveAt), rt.SessionID())
	}
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		term.Log("stopping (resume later with: eagent -c)")
		cancel()
		<-sigs
		os.Exit(130) // the agent's terminal outlives eagent; a resume finds it again
	}()
	code := rt.Run(ctx)
	cancel()
	st := rt.State()
	if *jsonOut {
		term.End(rt.SessionID(), st.EndReason, code, st.UsageLine())
	}
	term.Close(fmt.Sprintf("session %s ended (%s) · %s", rt.SessionID(), st.EndReason, st.UsageLine()))
	return code
}

// batchNeedsPrompt reports whether a non-interactive run is missing its
// prompt. One that names an agent (--relay, or `eagent relay NAME`) may start
// it with none: the session starts the agent and waits.
func batchNeedsPrompt(prompt, relay string) bool { return prompt == "" && relay == "" }

// viewIgnoredFlags says, on w, that `eagent view` ignores --actor and
// --task: the three-model design had a prompt per actor and per task, and
// there is one conversation now.
func viewIgnoredFlags(w io.Writer, actor, task string) {
	var given []string
	if actor != "" {
		given = append(given, "--actor")
	}
	if task != "" {
		given = append(given, "--task")
	}
	switch len(given) {
	case 1:
		fmt.Fprintf(w, "eagent view: there is one conversation now, so %s is ignored\n", given[0])
	case 2:
		fmt.Fprintf(w, "eagent view: there is one conversation now, so %s and %s are ignored\n", given[0], given[1])
	}
}

// parseInterleaved accepts flags anywhere on the command line, so
// `eagent show <id> --raw` and `eagent resume <id> --answer yes` both work.
// A literal `--` ends flag parsing.
func parseInterleaved(fs *flag.FlagSet, args []string) ([]string, error) {
	rest, _, err := parseArgs(fs, args)
	return rest, err
}

// parseArgs is parseInterleaved that also says how many of the positional
// arguments came before a "--". Everything after "--" is positional, flags
// included: a prompt that starts with "-" or reads "help" stays a prompt.
func parseArgs(fs *flag.FlagSet, args []string) ([]string, int, error) {
	var positional []string
	before := -1
	for len(args) > 0 {
		if args[0] == "--" {
			before = len(positional)
			positional = append(positional, args[1:]...)
			break
		}
		if err := fs.Parse(args); err != nil {
			return nil, 0, err
		}
		rest := fs.Args()
		// flag.Parse stops at a "--" and drops it.
		if n := len(args) - len(rest); n > 0 && args[n-1] == "--" {
			before = len(positional)
			positional = append(positional, rest...)
			break
		}
		args = rest
		if len(args) > 0 {
			positional = append(positional, args[0])
			args = args[1:]
		}
	}
	if before < 0 {
		before = len(positional)
	}
	return positional, before, nil
}

func readAll(f *os.File) ([]byte, error) {
	var out []byte
	buf := make([]byte, 64<<10)
	for {
		n, err := f.Read(buf)
		out = append(out, buf[:n]...)
		if err != nil {
			return out, nil
		}
	}
}

func fail(err error) int {
	fmt.Fprintln(os.Stderr, "eagent:", err)
	return 1
}

func resolveProject(dir string) (string, error) {
	if dir == "" {
		dir = "."
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !st.IsDir() {
		return "", fmt.Errorf("%s is not a directory", abs)
	}
	return abs, nil
}

// ---- subcommands -------------------------------------------------------------

func cmdSessions(project string) int {
	sessions, err := store.List(store.Root(project))
	if err != nil {
		return fail(err)
	}
	if len(sessions) == 0 {
		fmt.Println("no sessions in", store.Root(project))
		return 0
	}
	for _, s := range sessions {
		first := firstUserMessage(s.Path)
		status := "?"
		if evs, err := store.Read(s.Path); err == nil {
			st := state.Replay(evs)
			switch {
			case !st.Ended:
				status = "open"
			case st.EndReason == "done":
				status = "done"
			default:
				status = st.EndReason
			}
		}
		fmt.Printf("%s  %s  %-15s %2d file%s %7s  %s\n", s.ID, s.Started.Local().Format("2006-01-02 15:04"), status, s.Subsessions, plural(s.Subsessions), humanBytes(s.Size), clip(first, 60))
	}
	return 0
}

// cmdSummarize writes a fresh title and summary for stored sessions, from
// their logs, to their FinaleChat threads. A running session keeps its own
// thread current; this is for sessions from before eagent did that, and for
// checking what the summary prompt produces (--dry-run). It reads logs and
// never writes them. With --json each line carries the old title and
// summary too, so a run can be undone by hand.
// routeChain is a route and its fallbacks for calls made side by side: all
// use the current one, and the first to find it unroutable moves them all
// to the next.
type routeChain struct {
	mu      sync.Mutex
	routes  []llm.Endpoint
	clients []*llm.Client
	at      int
}

func newRouteChain(routes []llm.Endpoint) *routeChain {
	c := &routeChain{routes: routes}
	for _, ep := range routes {
		c.clients = append(c.clients, llm.NewClient(ep))
	}
	return c
}

func (c *routeChain) current() *llm.Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.clients[c.at]
}

// currentRoute is the route the calls use now: after a fallback took over,
// the fallback's.
func (c *routeChain) currentRoute() llm.Endpoint {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.routes[c.at]
}

// next moves past failed, reporting whether another route is left to try.
// A call that failed on a route already left behind just tries the current.
func (c *routeChain) next(failed *llm.Client) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.clients[c.at] != failed {
		return true
	}
	if c.at+1 >= len(c.clients) {
		return false
	}
	c.at++
	fmt.Fprintf(os.Stderr, "summarizer: %s is unavailable; switching to %s\n", c.routes[c.at-1], c.routes[c.at])
	return true
}

func cmdSummarize(project, preset, bundle string, refs []string, all, dryRun, asJSON, verbose bool) int {
	if all == (len(refs) > 0) {
		return fail(errors.New("usage: eagent summarize <session-id>... | --all [--dry-run] [--json]"))
	}
	cfg, err := config.LoadBundle(project, preset, bundle)
	if err != nil {
		return fail(err)
	}
	route := cfg.Model
	if cfg.Summarizer.Mode == "dedicated" {
		route = cfg.Summarizer.Route(cfg.Model)
	}
	routes, err := route.Routes()
	if err != nil {
		return fail(fmt.Errorf("summarizer: %w", err))
	}
	models := newRouteChain(routes)
	set, err := prompts.Load(project)
	if err != nil {
		return fail(err)
	}
	var client *finalechat.Client
	if !finalechat.Disabled() && cfg.Finalechat.Wanted() {
		if c, ok := finalechat.Resolve(cfg.Finalechat.TokenEnvName(), cfg.Finalechat.BaseURL); ok {
			c.UserAgent = "eagent/" + version
			client = c
		}
	}
	if client == nil && !dryRun {
		return fail(errors.New("summarize needs a FinaleChat token to write to (or --dry-run to only print)"))
	}
	root := store.Root(project)
	var sessions []store.Info
	if all {
		if sessions, err = store.List(root); err != nil {
			return fail(err)
		}
	} else {
		for _, ref := range refs {
			info, err := store.Resolve(root, ref)
			if err != nil {
				return fail(err)
			}
			sessions = append(sessions, info)
		}
	}
	// A few at a time: each is one read of a log and one model call, and a
	// project can hold hundreds. Results print in the sessions' own order.
	type outcome struct {
		res harness.SessionSummary
		err error
	}
	results := make([]chan outcome, len(sessions))
	slots := make(chan struct{}, 6)
	for i, info := range sessions {
		results[i] = make(chan outcome, 1)
		go func(done chan<- outcome, info store.Info) {
			slots <- struct{}{}
			defer func() { <-slots }()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			var res harness.SessionSummary
			var err error
			for {
				model := models.current()
				res, err = harness.SummarizeSession(ctx, model, set, client, project, info, dryRun)
				// A route that cannot serve the model at all (no access,
				// no credits) gives way to the configured fallback.
				var ae *llm.APIError
				if !errors.As(err, &ae) || !ae.Unroutable() || !models.next(model) {
					break
				}
			}
			done <- outcome{res, err}
		}(results[i], info)
	}
	code := 0
	var usage event.Usage
	sent := 0
	for i, info := range sessions {
		o := <-results[i]
		res, err := o.res, o.err
		usage.Input += res.Usage.Input
		usage.Output += res.Usage.Output
		if res.Sent {
			sent++
		}
		if verbose && !asJSON && res.Input != "" {
			// For work on the prompt: what the model read and what it said.
			fmt.Printf("---- %s read:\n%s---- and answered: %q / %q\n", info.ID, res.Input, res.ModelTitle, res.ModelSummary)
		}
		if asJSON {
			line := map[string]any{"session": res.Session, "title": res.Title, "summary": res.Description, "old_title": res.OldTitle, "old_summary": res.OldDesc, "thread": res.Thread, "sent": res.Sent}
			if res.Skipped != "" {
				line["skipped"] = res.Skipped
			}
			if err != nil {
				line["error"] = err.Error()
			}
			raw, _ := json.Marshal(line)
			fmt.Println(string(raw))
		} else {
			switch {
			case err != nil:
				fmt.Printf("%s  error: %v\n", info.ID, err)
			case res.Title == "" && res.Description == "":
				fmt.Printf("%s  unchanged (%s)\n", info.ID, res.Skipped)
			default:
				title := res.Title
				if title == "" {
					title = res.OldTitle + " (title kept)"
				}
				note := ""
				switch {
				case res.Sent:
					note = "  [sent]"
				case res.Skipped != "":
					note = "  [" + res.Skipped + "]"
				}
				fmt.Printf("%s  %s%s\n               %s\n", info.ID, title, note, res.Description)
			}
		}
		if err != nil {
			code = 1
		}
	}
	if !asJSON {
		fmt.Printf("%d session%s, %d thread%s updated, %d tokens in / %d out on %s\n", len(sessions), pluralS(len(sessions)), sent, pluralS(sent), usage.Input, usage.Output, models.currentRoute().Model)
	}
	return code
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func plural(n int) string {
	if n == 1 {
		return " "
	}
	return "s"
}

func firstUserMessage(path string) string {
	evs, err := store.Read(path)
	if err != nil {
		return ""
	}
	for _, ev := range evs {
		if ev.Type == event.UserMessage {
			var d event.UserMessageData
			_ = ev.Decode(&d)
			return d.Text
		}
	}
	return ""
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1fMB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0fkB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%dB", n)
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n-1] + "…"
	}
	return s
}

func cmdShow(project, ref string, raw bool, actor, task string) int {
	info, err := store.Resolve(store.Root(project), ref)
	if err != nil {
		return fail(err)
	}
	evs, err := store.Read(info.Path)
	if err != nil {
		return fail(err)
	}
	for _, ev := range evs {
		if actor != "" && ev.Actor != actor {
			continue
		}
		if task != "" && ev.Task != task {
			continue
		}
		if raw {
			line, _ := json.Marshal(ev)
			fmt.Printf("%s:%d %s\n", ev.Source.File, ev.Source.Line, line)
			continue
		}
		if actor != "" || task != "" {
			fmt.Printf("%s:%d %s\n", ev.Source.File, ev.Source.Line, tools.Summarize(ev, 600))
			continue
		}
		if line := transcriptLine(ev); line != "" {
			fmt.Println(line)
		}
	}
	return 0
}

// transcriptLine renders the events a human reads to follow a session.
func transcriptLine(ev event.Event) string {
	ts := ev.Time.Local().Format("15:04:05")
	cite := fmt.Sprintf("%s:%d", ev.Source.File, ev.Source.Line)
	block := func(label, text string) string {
		text = strings.TrimSpace(text)
		if !strings.Contains(text, "\n") && len(text) < 100 {
			return fmt.Sprintf("%s %-12s %s   (%s)", ts, label, text, cite)
		}
		return fmt.Sprintf("%s %-12s (%s)\n    %s\n", ts, label, cite, strings.ReplaceAll(text, "\n", "\n    "))
	}
	switch ev.Type {
	case event.SessionStart:
		var d event.SessionStartData
		_ = ev.Decode(&d)
		return fmt.Sprintf("%s session      %s in %s (%s)", ts, d.Session, d.Cwd, modelsLine(d.Models))
	case event.SessionResume:
		return fmt.Sprintf("%s session      resumed", ts)
	case event.SessionEnd:
		var d event.SessionEndData
		_ = ev.Decode(&d)
		return fmt.Sprintf("%s session      ended: %s", ts, d.Reason)
	case event.SubsessionStart:
		var d event.SubsessionStartData
		_ = ev.Decode(&d)
		switch d.Reason {
		case "rollover":
			return fmt.Sprintf("%s subsession   %d (%s) after context rollover", ts, d.Index+1, d.File)
		case "upgrade":
			// An older session continued by the one-loop eagent, from a
			// briefing written from its record.
			return fmt.Sprintf("%s subsession   %d (%s) continued by the one-loop eagent from a briefing", ts, d.Index+1, d.File)
		}
	case event.Dossier:
		var d event.DossierData
		_ = ev.Decode(&d)
		return block("dossier", d.Text)
	case event.UserMessage:
		var d event.UserMessageData
		_ = ev.Decode(&d)
		return block("USER", d.Text)
	case event.UserAnswer:
		var d event.UserAnswerData
		_ = ev.Decode(&d)
		return block("USER answer", d.Text)
	case event.UserDismiss:
		var d event.UserDismissData
		_ = ev.Decode(&d)
		return block("USER", "dismissed question "+d.QuestionID+" without answering")
	case event.Message:
		var d event.MessageData
		_ = ev.Decode(&d)
		return block("EAGENT", d.Text)
	case event.Question:
		var d event.QuestionData
		_ = ev.Decode(&d)
		text := d.Text
		for i, o := range d.Options {
			text += fmt.Sprintf("\n  %d) %s", i+1, o)
		}
		return block("EAGENT asks", text)
	case event.RelayStart:
		var d event.RelayStartData
		_ = ev.Decode(&d)
		what := strings.TrimSpace(d.Model + " " + d.Effort)
		if what != "" {
			what = " (" + what + ")"
		}
		verb := "started"
		if d.Resumed {
			verb = "resumed"
		}
		return fmt.Sprintf("%s agent        %s %s in %s%s   (%s)", ts, firstNonEmpty(d.AgentLabel, d.Agent, d.Name), verb, d.Dir, what, cite)
	case event.RelaySend:
		var d event.RelaySendData
		_ = ev.Decode(&d)
		return block("-> "+d.Name, d.Text)
	case event.RelayTurn:
		var d event.RelayTurnData
		_ = ev.Decode(&d)
		return block(strings.ToUpper(d.Name), d.Text)
	case event.RelayState:
		var d event.RelayStateData
		_ = ev.Decode(&d)
		if d.State == "blocked" || d.State == "exited" {
			return block(d.Name+" "+d.State, d.Detail)
		}
	case event.RelayEnd:
		var d event.RelayStateData
		_ = ev.Decode(&d)
		return fmt.Sprintf("%s agent        %s stopped   (%s)", ts, d.Name, cite)
	case event.LegacyNote:
		var d struct {
			Text string `json:"text"`
		}
		_ = ev.Decode(&d)
		return block("note", d.Text)
	case event.LegacyTaskCreate:
		var d struct {
			ID, Title, Description string
		}
		_ = ev.Decode(&d)
		return block("delegate "+d.ID, d.Title+"\n"+d.Description)
	case event.LegacyTaskEnd:
		var d struct {
			ID, Status, Summary string
		}
		_ = ev.Decode(&d)
		return block(d.ID+" "+d.Status, d.Summary)
	case event.LegacyYield:
		var d event.LegacyYieldData
		_ = ev.Decode(&d)
		if d.Done {
			return block("DONE", d.Reason)
		}
		return block("waiting", d.Reason)
	case event.Error:
		var d event.ErrorData
		_ = ev.Decode(&d)
		return block("ERROR "+d.Where, d.Text)
	case event.Route:
		var d event.RouteData
		_ = ev.Decode(&d)
		return fmt.Sprintf("%s route        %s -> %s (%s)", ts, d.Actor, d.Model, d.Reason)
	}
	return ""
}

// modelsLine names the models a session ran on: eagent's, or the three
// actors' in a session from before eagent was one loop.
func modelsLine(models map[string]string) string {
	if m := models[event.ActorEagent]; m != "" {
		return "eagent on " + m
	}
	var parts []string
	for _, a := range event.LegacyActors {
		if m := models[a]; m != "" {
			parts = append(parts, a+" "+m)
		}
	}
	return strings.Join(parts, " / ")
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}

func cmdReplay(project, ref string) int {
	info, err := store.Resolve(store.Root(project), ref)
	if err != nil {
		return fail(err)
	}
	start := time.Now()
	evs, err := store.Read(info.Path)
	if err != nil {
		return fail(err)
	}
	st := state.Replay(evs)
	fmt.Printf("replayed %d events from %d file%s in %s\n\n", len(evs), len(st.Subsessions), plural(len(st.Subsessions)), time.Since(start).Round(time.Millisecond))
	fmt.Println(st.Summary())
	fmt.Printf("\nstatus: ")
	switch {
	case !st.Ended:
		fmt.Println("open (no session.end; resume with: eagent resume", st.SessionID+")")
	default:
		fmt.Println(st.EndReason)
	}
	if st.Legacy() {
		fmt.Println("written before eagent was one loop; its tasks and processes are history only")
	}
	fmt.Printf("eagent's conversation: %d messages\n", len(st.View(nil)))
	if len(st.Errors) > 0 {
		fmt.Printf("\nerrors recorded: %d\n", len(st.Errors))
		for _, e := range st.Errors[max(0, len(st.Errors)-5):] {
			fmt.Println("  " + clip(e, 200))
		}
	}
	return 0
}

func cmdDoctor(project, preset, bundle string, live bool) int {
	cfg, err := config.LoadBundle(project, preset, bundle)
	if err != nil {
		return fail(err)
	}
	ok := true
	fmt.Printf("project: %s\n", project)
	if cfg.Name != "" {
		fmt.Printf("config bundle: %s\n", cfg.Name)
	}
	if cfg.Preset != "" {
		fmt.Printf("preset: %s\n", cfg.Preset)
	}
	if _, err := os.Stat(config.File(project)); err == nil {
		fmt.Printf("config: %s\n", config.File(project))
	} else {
		fmt.Println("config: defaults (no .agents/eagent/config.json)")
	}
	if cfg.Instructions != "" {
		fmt.Printf("project instructions: %d chars loaded\n", len(cfg.Instructions))
	}
	fc := cfg.Finalechat
	if !fc.Wanted() {
		fmt.Println("finalechat: off (disabled in config)")
	} else if c, found := finalechat.Resolve(fc.TokenEnvName(), fc.BaseURL); !found {
		if fc.Required() {
			ok = false
			fmt.Printf("  ✗ finalechat: enabled in config but no token in $%s or ~/.config/finalechat/config.json\n", fc.TokenEnvName())
		} else {
			fmt.Printf("finalechat: off (no token in $%s or ~/.config/finalechat/config.json; create one under Settings → Agents at https://www.finalechat.com)\n", fc.TokenEnvName())
		}
	} else if live {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		me, err := c.Me(ctx)
		cancel()
		if err != nil {
			ok = false
			fmt.Printf("  ✗ finalechat: token from %s rejected: %v\n", c.Source, err)
		} else {
			mode := ""
			if me.User.Settings.RemoteMode {
				mode = ", remote mode on"
			}
			fmt.Printf("  ✓ finalechat: on, signed in as %s (token from %s%s)\n", me.User.DisplayName, c.Source, mode)
		}
	} else {
		fmt.Printf("finalechat: on (token from %s; --live checks it)\n", c.Source)
	}
	if n, how := cfg.RolloverChosen(project); cfg.RolloverTokens > 0 {
		fmt.Printf("conversation restarts at: %d prompt tokens, %s\n", n, how)
	} else {
		fmt.Printf("conversation restarts at: %d prompt tokens, %s (rollover_tokens fixes a number)\n", n, how)
	}
	// Keys and environment variables of the three-model design: what was
	// carried over, what changed, and what is no longer read.
	for _, m := range cfg.Migrated {
		fmt.Printf("older configuration: %s\n", m)
	}
	// The load already failed on the configuration's errors; its warnings
	// are what the editor would show.
	for _, p := range cfg.Problems() {
		if p.Severity != "error" {
			fmt.Printf("  ! config %s: %s\n", strings.TrimPrefix(p.Path, "/"), p.Message)
		}
	}
	check := func(name string, a config.ModelRoute) {
		routes, err := a.Routes()
		if err != nil {
			ok = false
			fmt.Printf("  ✗ %-12s %s: %v\n", name, a.Model, err)
			return
		}
		// Label by position in the configured chain and say why a hop is
		// missing, so a fallback that took over is never shown as the primary.
		var labels []string
		for cur, i := &a, 0; cur != nil; cur, i = cur.Fallback, i+1 {
			label := "  ✓"
			if i > 0 {
				label = "    fallback"
			}
			if _, e := cur.Endpoint(); e != nil {
				fmt.Printf("  ! %-12s %s: %v\n", name, cur.Model, e)
				continue
			}
			labels = append(labels, label)
		}
		anyLive := false
		for i, ep := range routes {
			label := "  ✓"
			if i < len(labels) {
				label = labels[i]
			}
			line := fmt.Sprintf("%s %-12s %s (%s", label, name, ep, ep.Protocol)
			if ep.ReasoningEffort != "" {
				line += ", effort " + ep.ReasoningEffort
			}
			line += ")"
			if live {
				start := time.Now()
				c := llm.NewClient(ep)
				c.MaxAttempts = 1
				ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
				resp, err := c.Complete(ctx, llm.Request{
					System:    "Reply with a single tool call.",
					Messages:  []llm.Message{{Role: "user", Text: "Call ping with message=\"pong\"."}},
					Tools:     []llm.Tool{{Name: "ping", Description: "Ping.", Parameters: json.RawMessage(`{"type":"object","properties":{"message":{"type":"string"}},"required":["message"]}`)}},
					MaxTokens: 4096,
				}, nil)
				cancel()
				switch {
				case err != nil:
					line += fmt.Sprintf("\n      ✗ live call failed: %v", clip(err.Error(), 200))
				case len(resp.ToolCalls) == 0:
					anyLive = true
					line += fmt.Sprintf("\n      ! live call ok in %s but no tool call was returned (text: %q)", time.Since(start).Round(time.Millisecond), clip(resp.Text, 80))
				default:
					anyLive = true
					line += fmt.Sprintf("\n      ✓ live tool call ok in %s (%d in / %d out tokens)", time.Since(start).Round(time.Millisecond), resp.Usage.Input, resp.Usage.Output)
				}
			}
			fmt.Println(line)
		}
		if live && !anyLive {
			ok = false
			fmt.Printf("      no working route for %s\n", name)
		} else if live && len(routes) > 1 {
			fmt.Printf("      (a failing primary route is fine while a fallback works; the switch is automatic)\n")
		}
	}
	fmt.Println("models:")
	check("eagent", cfg.Model)
	switch {
	case cfg.Summarizer.Mode != "dedicated":
		fmt.Println("  summarizer   inline (eagent keeps the title and summary itself)")
	case cfg.Summarizer.Model != nil && cfg.Summarizer.Model.Model != "":
		check("summarizer", *cfg.Summarizer.Model)
	default:
		fmt.Println("  summarizer   dedicated, on eagent's model")
	}
	fmt.Println("agents:")
	profiles := cfg.Relay.RelayProfiles()
	anyAgent := false
	for _, name := range cfg.Relay.RelayProfileNames() {
		p := profiles[name]
		mark := "  ✓"
		if !relay.Installed(p.Agent) {
			mark = "  -"
		} else {
			anyAgent = true
		}
		line := fmt.Sprintf("%s %-12s %s", mark, name, relay.AgentName(p.Agent))
		if !relay.Installed(p.Agent) {
			line += " (not installed)"
		}
		if name == cfg.Relay.Default {
			line += " (default)"
		}
		fmt.Println(line)
	}
	if d := cfg.Relay.Default; d != "" {
		if p, ok := profiles[d]; !ok {
			ok = false
			fmt.Printf("  ✗ relay.default %q is not a profile\n", d)
		} else if !relay.Installed(p.Agent) {
			ok = false
			fmt.Printf("  ✗ relay.default %q runs %s, which is not installed\n", d, relay.AgentName(p.Agent))
		}
	} else if !anyAgent {
		ok = false
		fmt.Println("  ✗ no agent is installed; a session needs one")
	}
	// A prompt override that does not parse stops every session at its
	// start; an override of a prompt of the three-model design is read by
	// nothing.
	fmt.Println("prompts:")
	if set, err := prompts.Load(project); err != nil {
		ok = false
		fmt.Printf("  ✗ %v\n", err)
	} else {
		overridden := false
		for _, name := range prompts.Names {
			if src := set.Source[name]; src != "built-in" {
				overridden = true
				fmt.Printf("  ✓ %-17s %s\n", name, src)
			}
		}
		if !overridden {
			fmt.Println("  ✓ built-in (no overrides in " + prompts.Dir(project) + ")")
		}
	}
	for _, p := range prompts.Ignored(project) {
		fmt.Printf("  ! %s is no longer read: eagent runs one model now (`eagent prompts` lists the prompts it reads)\n", p)
	}
	if servers, err := config.LoadServers(project); err == nil && len(servers) > 0 {
		fmt.Println("registered servers:")
		for _, s := range servers {
			protos := s.Protocol
			if len(s.Protocols) > 0 {
				protos = strings.Join(s.Protocols, ", ")
			}
			line := fmt.Sprintf("  ✓ %-12s %s (%s)", s.ID, s.BaseURL, protos)
			if live {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				res, perr := config.ProbeServer(ctx, config.ProbeOptions{
					URL: s.BaseURL, Protocol: s.Protocol, KeyEnv: s.KeyEnv, ID: s.ID, Label: s.Label,
				})
				cancel()
				if perr != nil {
					line = fmt.Sprintf("  ! %-12s %s: %v", s.ID, s.BaseURL, clip(perr.Error(), 160))
				} else {
					line += fmt.Sprintf(" · %d live model%s", len(res.Models), pluralS(len(res.Models)))
				}
			}
			fmt.Println(line)
		}
	}
	_, tmuxErr := lookPath("tmux")
	_, screenErr := lookPath("screen")
	if tmuxErr != nil && screenErr != nil {
		ok = false
		fmt.Println("  ✗ neither tmux nor screen is on PATH; the agent runs in one of them")
	}
	fmt.Printf("sessions: %s\n", store.Root(project))
	if !ok {
		fmt.Println("\nsome checks failed")
		return 1
	}
	fmt.Println("\nall good")
	return 0
}

func lookPath(name string) (string, error) {
	for _, dir := range strings.Split(os.Getenv("PATH"), ":") {
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", errors.New("not found")
}

func cmdConfig(project, preset, bundle string, write bool, args []string) int {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
		args = args[1:]
	}
	switch sub {
	case "list":
		cfg, _ := config.LoadBundle(project, preset, bundle)
		fmt.Println("built-in presets:")
		for _, name := range config.PresetNames() {
			mark := "  "
			if cfg.Preset == name && cfg.Name == "" || (cfg.Preset == "" && name == "glm" && cfg.Name == "") {
				mark = "* "
			}
			fmt.Printf("  %s%-10s %s\n", mark, name, config.PresetDescription(name))
		}
		names, err := config.ListBundles(project)
		if err != nil {
			return fail(err)
		}
		fmt.Printf("\nsaved presets in %s:\n", config.BundlesDir(project))
		if len(names) == 0 {
			fmt.Println("  (none yet; create one with: eagent presets save NAME \"description\")")
		}
		for _, name := range names {
			b, err := config.LoadBundle(project, "", name)
			mark := "  "
			if cfg.Name == name {
				mark = "* "
			}
			if err != nil {
				fmt.Printf("  %s%-10s (invalid: %v)\n", mark, name, err)
				continue
			}
			fmt.Printf("  %s%-10s %s\n", mark, name, describe(b))
		}
		fmt.Println("\nselect with --preset NAME, --config NAME, EAGENT_CONFIG=NAME, or `eagent presets use NAME`")
		return 0
	case "save":
		if len(args) < 1 {
			return fail(errors.New("usage: eagent presets save NAME [description]"))
		}
		// The configuration in force as every save keeps it.
		cfg, err := settings.CurrentLayers(project, preset, bundle)
		if err != nil {
			return fail(err)
		}
		if err := cfg.Validate(); err != nil {
			return fail(err)
		}
		cfg.Preset = ""
		path, err := config.SaveBundle(project, args[0], strings.Join(args[1:], " "), cfg)
		if err != nil {
			return fail(err)
		}
		fmt.Println("saved", path)
		fmt.Printf("use it with: eagent --preset %s ...   (or --config %s, or `eagent presets use %s`)\n", args[0], args[0], args[0])
		return 0
	case "delete", "rm":
		if len(args) < 1 {
			return fail(errors.New("usage: eagent presets delete NAME"))
		}
		if err := config.DeleteBundle(project, args[0]); err != nil {
			return fail(err)
		}
		fmt.Printf("deleted preset %s\n", args[0])
		return 0
	case "use":
		if len(args) < 1 {
			return fail(errors.New("usage: eagent presets use NAME|default"))
		}
		target := strings.TrimSpace(args[0])
		editor, err := config.LockEditor(context.Background(), project)
		if err != nil {
			return fail(err)
		}
		defer editor.Close()
		file := config.Resolve(project, "", "").File
		edits := map[string]json.RawMessage{}
		dropped, moved := "", ""
		var unloadable error // the file as left does not load here
		switch {
		case target == "default" || target == "none" || target == "-":
			edits["default_config"] = nil
		default:
			if canon, ok := config.ResolvePreset(target); ok {
				edits["preset"], _ = json.Marshal(canon)
				edits["default_config"] = nil
				// The preset's model is what this chooses: a model the
				// file names would run instead of it, so it goes. The
				// file's effort and reply ceiling stay.
				if file.Raw != "" {
					var m map[string]json.RawMessage
					if err := json.Unmarshal([]byte(file.Raw), &m); err != nil {
						return fail(fmt.Errorf("%s: %w", file.Path, err))
					}
					var rest map[string]json.RawMessage
					rest, dropped = config.DropModelChoice(m)
					var was, left map[string]json.RawMessage
					migrated, _ := config.MigrateLegacy(m)
					_ = json.Unmarshal(migrated["model"], &was)
					_ = json.Unmarshal(rest["model"], &left)
					if len(left) < len(was) {
						// Part of the file's route goes (its model, or
						// only where one is reached) so that the preset's
						// runs. Where it would not run even so, nothing is
						// written: loading the file as it would be left
						// says which model runs here.
						after := maps.Clone(rest)
						after["preset"] = edits["preset"]
						delete(after, "default_config")
						want := config.Defaults()
						config.Presets[canon](&want)
						now, err := config.LoadBundleWithFile(project, "", "", after)
						if err != nil {
							// (Said in place of "the preset's model runs".)
							unloadable = err
						} else if otherRoute(now.Model, want.Model) {
							// All that stands in the way, so that one pass
							// clears it: each variable that sets a part of
							// the route that differs, the saved preset laid
							// over the file, and another preset that a
							// variable names.
							var why []string
							for field, d := range map[string]bool{
								"model":       now.Model.Model != want.Model.Model,
								"base_url":    strings.TrimRight(now.Model.BaseURL, "/") != strings.TrimRight(want.Model.BaseURL, "/"),
								"protocol":    now.Model.Protocol != want.Model.Protocol,
								"api_key_env": now.Model.APIKeyEnv != want.Model.APIKeyEnv,
							} {
								if name, ok := config.EnvOverrides()["/model/"+field]; ok && d {
									why = append(why, fmt.Sprintf("%s sets eagent's %s here", name, field))
								}
							}
							slices.Sort(why)
							if now.Name != "" {
								why = append(why, fmt.Sprintf("%s puts the saved preset %q over the project file here", now.NameFrom, now.Name))
							}
							if laid, who := presetAbove(project, ""); laid != "" && presetsDiffer(laid, canon) {
								why = append(why, fmt.Sprintf("%s names the preset %s here", who, laid))
							}
							if len(why) == 0 {
								why = []string{"another setting names eagent's model here"}
							}
							return fail(fmt.Errorf("%s, so %s would not run as the preset has it (%s at %s with $%s would); nothing was changed. Unset that first", strings.Join(why, ", and "), target, now.Model.Model, now.Model.BaseURL, now.Model.APIKeyEnv))
						}
					}
					// An effort the preset's model does not offer moves
					// to the nearest it does, as choosing the preset in a
					// session moves it.
					var route map[string]json.RawMessage
					var kept string
					if json.Unmarshal(rest["model"], &route) == nil && json.Unmarshal(route["reasoning_effort"], &kept) == nil && kept != "" {
						cfg := config.Defaults()
						config.Presets[canon](&cfg)
						cfg.Model.ReasoningEffort = kept
						config.OfferedEffort(project, &cfg)
						if cfg.Model.ReasoningEffort != kept {
							route["reasoning_effort"], _ = json.Marshal(cfg.Model.ReasoningEffort)
							rest["model"], _ = json.Marshal(route)
							moved = kept + " to " + cfg.Model.ReasoningEffort
						}
					}
					edits["model"] = rest["model"]
				}
			} else {
				if _, err := config.LoadBundle(project, "", target); err != nil {
					return fail(err)
				}
				edits["default_config"], _ = json.Marshal(target)
			}
		}
		res, err := editor.SaveFile(edits, file.ETag, true)
		if err != nil {
			return fail(err)
		}
		fmt.Printf("updated %s\n", res.Path)
		if dropped != "" && unloadable != nil {
			fmt.Printf("  the model the file named (%s) was removed; the previous file is in %s.bak. The configuration does not load here, so no session starts until that is put right: %v\n", dropped, res.Path, unloadable)
		} else if dropped != "" {
			fmt.Printf("  the model the file named (%s) was removed, so %s's model runs; the previous file is in %s.bak\n", dropped, target, res.Path)
		}
		if moved != "" {
			fmt.Printf("  the effort moved from %s, the nearest %s's model offers\n", moved, target)
		}
		return 0
	case "show":
		if len(args) < 1 {
			return fail(errors.New("usage: eagent config show NAME"))
		}
		bundle = args[0]
	case "":
	default:
		return fail(fmt.Errorf("unknown config command %q (list, save, show, use, delete)", sub))
	}
	cfg, err := config.LoadBundle(project, preset, bundle)
	if err != nil {
		return fail(err)
	}
	if write {
		// The file holds what the files and a bundle say, as every save
		// keeps the configuration in force, never what a variable gives.
		if cfg, err = settings.CurrentLayers(project, preset, bundle); err != nil {
			return fail(err)
		}
		cfg.Name = "" // config.json is no bundle: a name there would read as one laid
		path, err := config.Write(project, cfg)
		if err != nil {
			return fail(err)
		}
		fmt.Println("wrote", path)
		return 0
	}
	raw, _ := json.MarshalIndent(cfg, "", "  ")
	fmt.Println(string(raw))
	return 0
}

// describe renders a bundle's models in one line.
func describe(c config.Config) string {
	s := shortModel(c.Model.Model)
	if c.Description != "" {
		s += " — " + c.Description
	}
	return s
}

func shortModel(m string) string {
	if i := strings.LastIndexByte(m, '/'); i >= 0 {
		return m[i+1:]
	}
	return m
}

func cmdPrompts(project string, args []string) int {
	set, err := prompts.Load(project)
	if err != nil {
		return fail(err)
	}
	if len(args) == 0 || args[0] == "list" {
		fmt.Printf("prompt overrides directory: %s\n\n", prompts.Dir(project))
		for _, name := range prompts.Names {
			fmt.Printf("  %-22s %s\n", name, set.Source[name])
		}
		fmt.Println("\n`eagent prompts export` copies the built-in defaults there for editing; `eagent prompts show NAME` prints one.")
		return 0
	}
	switch args[0] {
	case "export":
		written, err := prompts.Export(project)
		if err != nil {
			return fail(err)
		}
		if len(written) == 0 {
			fmt.Println("nothing to do: all prompt files already exist in", prompts.Dir(project))
			return 0
		}
		for _, p := range written {
			fmt.Println("wrote", p)
		}
		return 0
	case "show":
		if len(args) < 2 {
			return fail(errors.New("usage: eagent prompts show NAME"))
		}
		name := strings.ToUpper(strings.TrimSuffix(args[1], ".md")) + ".md"
		if set.Text(name) == "" {
			return fail(fmt.Errorf("unknown prompt %q (have %s)", args[1], strings.Join(prompts.Names, ", ")))
		}
		fmt.Print(set.Text(name))
		return 0
	}
	return fail(fmt.Errorf("unknown prompts command %q", args[0]))
}

// stdinIsTerminal reports whether standard input is a terminal, so a run
// without -p can prompt the user. A pipe or a file is not a character
// device; /dev/null is, but unlike a terminal it is seekable.
func stdinIsTerminal() bool {
	fi, err := os.Stdin.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	if _, err := os.Stdin.Seek(0, io.SeekCurrent); err == nil {
		return false
	}
	return true
}

func displayAddr(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "localhost" + addr
	}
	return addr
}

func cmdServe(project, addr, preset, bundle string, verbose bool) int {
	ws := web.New(project, func(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) })
	ws.Preset, ws.Bundle, ws.Verbose = preset, bundle, verbose
	ctx, cancel := context.WithCancel(context.Background())
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		fmt.Fprintln(os.Stderr, "stopping (Ctrl+C again to quit now)")
		cancel()
		<-sigs
		fmt.Fprintln(os.Stderr, "quitting")
		os.Exit(130)
	}()
	fmt.Fprintf(os.Stderr, "eagent web UI for %s\n  http://%s/\n", project, displayAddr(addr))
	if err := web.ListenAndServe(ctx, addr, ws); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fail(err)
	}
	return 0
}

// cmdView renders eagent's prompt from the log, the way the runtime would:
// its system prompt and the conversation up to an event. (The few lines a
// call adds at its end for that call only are not in the log.)
func cmdView(project, ref string, until int64, asJSON bool) int {
	info, err := store.Resolve(store.Root(project), ref)
	if err != nil {
		return fail(err)
	}
	evs, err := store.Read(info.Path)
	if err != nil {
		return fail(err)
	}
	if until > 0 {
		var cut []event.Event
		for _, ev := range evs {
			if ev.Seq <= until {
				cut = append(cut, ev)
			}
		}
		evs = cut
	}
	st := state.Replay(evs)
	set, err := prompts.Load(project)
	if err != nil {
		return fail(err)
	}
	st.SetPrompts(set)
	cfg, _ := config.Load(project, "")
	persona := strings.TrimSpace(cfg.Persona)
	if persona == "" {
		persona = strings.TrimSpace(set.Render("PERSONA.md", nil))
	}
	phone := ""
	if st.Phone != nil {
		phone = harness.PhoneStatus()
	}
	system := set.Render("EAGENT.md", prompts.EagentData{Persona: persona, Phone: phone, Project: st.Cwd, Interactive: st.Interactive, Instructions: strings.TrimSpace(cfg.Instructions)})
	view := state.DefaultViewOptions
	if cfg.ReportMaxChars > 0 {
		view.ReportChars = cfg.ReportMaxChars
	}
	msgs := st.View(&view)
	if asJSON {
		type msg struct {
			Role      string           `json:"role"`
			Text      string           `json:"text,omitempty"`
			ToolCalls []event.ToolCall `json:"tool_calls,omitempty"`
			Results   []llm.ToolResult `json:"results,omitempty"`
		}
		out := struct {
			System   string `json:"system"`
			Messages []msg  `json:"messages"`
		}{System: system}
		for _, m := range msgs {
			out.Messages = append(out.Messages, msg{Role: m.Role, Text: m.Text, ToolCalls: m.ToolCalls, Results: m.Results})
		}
		raw, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(raw))
		return 0
	}
	fmt.Printf("=== system (%d chars) ===\n%s\n", len(system), system)
	for i, m := range msgs {
		fmt.Printf("\n=== %d %s ===\n", i+1, m.Role)
		if m.Text != "" {
			fmt.Println(m.Text)
		}
		for _, tc := range m.ToolCalls {
			fmt.Printf("[tool call %s %s %s]\n", tc.ID, tc.Name, string(tc.Args))
		}
		for _, r := range m.Results {
			fmt.Printf("[result %s %s] %s\n", r.CallID, r.Name, r.Output)
		}
	}
	return 0
}
