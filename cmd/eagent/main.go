// Command eagent runs a three-actor, event-sourced coding agent.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/finalechat"
	"github.com/ericflo/eagent/internal/harness"
	"github.com/ericflo/eagent/internal/llm"
	"github.com/ericflo/eagent/internal/prompts"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/store"
	"github.com/ericflo/eagent/internal/tools"
	"github.com/ericflo/eagent/internal/ui"
	"github.com/ericflo/eagent/internal/web"
)

var version = "0.1.0"

const usage = `eagent — a three-actor, event-sourced coding agent in one binary

Usage:
  eagent [flags] [prompt]            start an interactive session (prompt optional)
  eagent -p [flags] "prompt"         batch mode: run until the work is done, then exit
  eagent -c [flags] [prompt]         continue the most recent session in this project
  eagent resume [flags] <id> [prompt]
  eagent sessions                    list sessions in this project
  eagent show <id> [--raw] [--actor orchestrator|task|narrator] [--task tN]
  eagent replay <id>                 rebuild state from the log and print it
  eagent artifact export <id> --output DIR
                                     export a portable website and native source logs
  eagent artifact publish <id>       publish a saved session to FinaleChat
  eagent artifact verify DIR         verify a downloaded archive without executing it
  eagent artifact restore DIR --output NEW_DIR
                                    recover native sources into a new directory
  eagent artifact enable|disable     opt this project into/out of proactive archives
  eagent connector pair             request remote settings access in FinaleChat
  eagent connector run              keep remote settings available without a session
  eagent serve [--addr 127.0.0.1:7331]  web UI: live chat, session browser, tasks, tool calls, config
  eagent doctor [--live]             check configuration and credentials
  eagent config                      print the effective configuration
  eagent config list                 built-in presets and the project's named bundles
  eagent config save NAME [desc]     save the effective configuration as a named bundle
  eagent config show NAME            print a bundle's effective configuration
  eagent prompts [export|show NAME]  list, export, or print the actor prompts
  eagent view <id> [--actor narrator|orchestrator|task --task tN] [--until SEQ]
                                     print the exact prompt an actor would receive (for prompt work)
  eagent version

Flags (before positional arguments):
  -p            non-interactive: exit when idle (code 0 done, 2 awaiting input)
  -c            resume the latest session
  -v            verbose: show every tool call and model response
  --json        emit narrator output as JSON lines on stdout
  -C <dir>      project directory (default: current directory)
  --preset <n>  built-in model preset: glm (default), astra, deepseek, qwen, or a
                provider tier: openai-high|med|low, openrouter-high|med|low,
                anthropic-high|med, deepinfra-high|med|low, fireworks-high|med|low,
                opencode-high|med|low, nous-high|med|low
  --config <n>  named bundle from .agents/eagent/configs/ (or EAGENT_CONFIG)
  --answer <s>  answer the pending question when resuming
  --serve <a>   also serve the web UI at this address while a session runs (e.g. 127.0.0.1:7331)

Credentials come from the environment: TOGETHER_API_KEY (default models),
OPENAI_API_KEY / OPENROUTER_API_KEY (openai-* and astra presets, OpenRouter is the
fallback), OPENROUTER_API_KEY (openrouter-*, qwen), ANTHROPIC_API_KEY (anthropic-*),
DEEPINFRA_API_KEY, FIREWORKS_API_KEY, OPENCODE_ZEN_API_KEY, NOUS_API_KEY.
Sessions are stored in <project>/.agents/eagent/sessions/.
`

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	fs := flag.NewFlagSet("eagent", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	var (
		batch    = fs.Bool("p", false, "non-interactive")
		cont     = fs.Bool("c", false, "continue latest session")
		verbose  = fs.Bool("v", false, "verbose")
		jsonOut  = fs.Bool("json", false, "json output")
		dir      = fs.String("C", "", "project directory")
		preset   = fs.String("preset", "", "model preset")
		bundle   = fs.String("config", "", "named config bundle")
		addr     = fs.String("addr", "127.0.0.1:7331", "serve: listen address")
		serveAt  = fs.String("serve", "", "serve the web UI while running")
		answer   = fs.String("answer", "", "answer to the pending question")
		showRaw  = fs.Bool("raw", false, "show raw events")
		actor    = fs.String("actor", "", "filter by actor")
		task     = fs.String("task", "", "filter by task id")
		until    = fs.Int64("until", 0, "view: only events up to this seq")
		asJSON   = fs.Bool("as-json", false, "view: machine-readable output")
		live     = fs.Bool("live", false, "doctor: make a small live call per actor")
		write    = fs.Bool("write", false, "config: write config.json")
		output   = fs.String("output", "", "artifact export/restore: new destination directory")
		helpFlag = fs.Bool("h", false, "help")
	)
	fs.BoolVar(helpFlag, "help", false, "help")
	rest, err := parseInterleaved(fs, args)
	if err != nil {
		return 64 // EX_USAGE
	}
	if *helpFlag {
		fmt.Fprint(os.Stdout, usage)
		return 0
	}
	harness.Version = version

	project, err := resolveProject(*dir)
	if err != nil {
		return fail(err)
	}

	cmd := ""
	if len(rest) > 0 {
		switch rest[0] {
		case "sessions", "show", "replay", "resume", "doctor", "config", "prompts", "serve", "view", "version", "help", "artifact", "connector":
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
	case "prompts":
		return cmdPrompts(project, rest)
	case "serve":
		return cmdServe(project, *addr, *preset, *bundle, *verbose)
	case "artifact":
		return cmdArtifact(project, rest, *output)
	case "connector":
		return cmdConnector(project, rest)
	case "view":
		if len(rest) < 1 {
			return fail(errors.New("usage: eagent view <session-id> [--actor narrator] [--until SEQ]"))
		}
		return cmdView(project, rest[0], *actor, *task, *until, *asJSON)
	}

	cfg, err := config.LoadBundle(project, *preset, *bundle)
	if err != nil {
		return fail(err)
	}
	// Interactive only when someone can actually type: a pipe, a file, or
	// /dev/null on stdin would otherwise read as an immediate "quit" and the
	// run would exit 0 having done nothing.
	opts := harness.Options{Project: project, Interactive: !*batch && stdinIsTerminal(), Verbose: *verbose, Answer: *answer}
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
		if opts.Prompt == "" {
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
	term.Banner(rt.SessionID(), map[string]string{
		"orchestrator": cfg.Orchestrator.Model, "task": cfg.Task.Model, "narrator": cfg.Narrator.Model,
	}, sessionPath != "")
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
		rt.KillProcesses() // do not leave children running on a hard exit
		os.Exit(130)
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

// parseInterleaved accepts flags anywhere on the command line, so
// `eagent show <id> --raw` and `eagent resume <id> --answer yes` both work.
// A literal `--` ends flag parsing.
func parseInterleaved(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for len(args) > 0 {
		if args[0] == "--" {
			positional = append(positional, args[1:]...)
			break
		}
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) > 0 {
			positional = append(positional, args[0])
			args = args[1:]
		}
	}
	return positional, nil
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
		return fmt.Sprintf("%s session      %s in %s (%s / %s / %s)", ts, d.Session, d.Cwd, d.Models["orchestrator"], d.Models["task"], d.Models["narrator"])
	case event.SessionResume:
		return fmt.Sprintf("%s session      resumed", ts)
	case event.SessionEnd:
		var d event.SessionEndData
		_ = ev.Decode(&d)
		return fmt.Sprintf("%s session      ended: %s", ts, d.Reason)
	case event.SubsessionStart:
		var d event.SubsessionStartData
		_ = ev.Decode(&d)
		if d.Reason == "rollover" {
			return fmt.Sprintf("%s subsession   %d (%s) after context rollover", ts, d.Index+1, d.File)
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
	case event.NarratorMessage:
		var d event.NarratorMessageData
		_ = ev.Decode(&d)
		return block("NARRATOR", d.Text)
	case event.NarratorQuestion:
		var d event.NarratorQuestionData
		_ = ev.Decode(&d)
		text := d.Text
		for i, o := range d.Options {
			text += fmt.Sprintf("\n  %d) %s", i+1, o)
		}
		return block("NARRATOR asks", text)
	case event.Note:
		var d event.NoteData
		_ = ev.Decode(&d)
		return block("note", d.Text)
	case event.TaskCreate:
		var d event.TaskCreateData
		_ = ev.Decode(&d)
		return block("delegate "+d.ID, d.Title+"\n"+d.Description)
	case event.TaskEnd:
		var d event.TaskEndData
		_ = ev.Decode(&d)
		return block(d.ID+" "+d.Status, d.Summary)
	case event.Yield:
		var d event.YieldData
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
	fmt.Printf("orchestrator view: %d messages; narrator view: %d messages\n", len(st.OrchestratorView()), len(st.NarratorView(nil)))
	for _, id := range st.TaskOrder {
		t := st.Tasks[id]
		fmt.Printf("  %s [%s] %s — %d calls\n", t.ID, t.Status, clip(t.Title, 60), t.Turns)
	}
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
	check := func(name string, a config.Actor) {
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
					MaxTokens: 200,
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
	check("orchestrator", cfg.Orchestrator)
	check("task", cfg.Task)
	check("narrator", cfg.Narrator)
	if _, err := os.Stat("/bin/bash"); err != nil {
		if _, err := lookPath("bash"); err != nil {
			ok = false
			fmt.Println("  ✗ bash not found on PATH")
		}
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
		fmt.Printf("\nproject bundles in %s:\n", config.BundlesDir(project))
		if len(names) == 0 {
			fmt.Println("  (none yet; create one with: eagent config save NAME \"description\")")
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
		fmt.Println("\nselect with --config NAME, EAGENT_CONFIG=NAME, or \"default_config\" in .agents/eagent/config.json")
		return 0
	case "save":
		if len(args) < 1 {
			return fail(errors.New("usage: eagent config save NAME [description]"))
		}
		cfg, err := config.LoadBundle(project, preset, bundle)
		if err != nil {
			return fail(err)
		}
		path, err := config.SaveBundle(project, args[0], strings.Join(args[1:], " "), cfg)
		if err != nil {
			return fail(err)
		}
		fmt.Println("saved", path)
		fmt.Printf("use it with: eagent --config %s ...   (or EAGENT_CONFIG=%s)\n", args[0], args[0])
		return 0
	case "show":
		if len(args) < 1 {
			return fail(errors.New("usage: eagent config show NAME"))
		}
		bundle = args[0]
	case "":
	default:
		return fail(fmt.Errorf("unknown config command %q (list, save, show)", sub))
	}
	cfg, err := config.LoadBundle(project, preset, bundle)
	if err != nil {
		return fail(err)
	}
	if write {
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
	s := fmt.Sprintf("%s / %s / %s", shortModel(c.Orchestrator.Model), shortModel(c.Task.Model), shortModel(c.Narrator.Model))
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
	go func() { <-sigs; fmt.Fprintln(os.Stderr, "stopping"); cancel() }()
	fmt.Fprintf(os.Stderr, "eagent web UI for %s\n  http://%s/\n", project, displayAddr(addr))
	if err := web.ListenAndServe(ctx, addr, ws); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fail(err)
	}
	return 0
}

// cmdView renders an actor's prompt from the log, the way the runtime would.
func cmdView(project, ref, actor, task string, until int64, asJSON bool) int {
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
	cfg, _ := config.Load(project, "")
	if actor == "" {
		actor = event.ActorNarrator
	}
	var system string
	var msgs []llm.Message
	switch actor {
	case event.ActorNarrator:
		persona := strings.TrimSpace(cfg.Persona)
		if persona == "" {
			persona = strings.TrimSpace(set.Render("PERSONA.md", nil))
		}
		phone := ""
		if st.Phone != nil {
			phone = harness.PhoneStatus(st.Phone.RemoteMode)
		}
		system = set.Render("NARRATOR.md", prompts.NarratorData{Persona: persona, Phone: phone})
		msgs = st.NarratorView(nil)
	case event.ActorOrchestrator:
		system = set.Render("ORCHESTRATOR.md", prompts.OrchestratorData{Project: st.Cwd, Instructions: cfg.Instructions, Interactive: st.Interactive})
		msgs = st.OrchestratorView()
	case event.ActorTask:
		if task == "" {
			return fail(errors.New("--task is required for the task view"))
		}
		system = set.Render("TASK-WORKER.md", prompts.TaskData{Project: st.Cwd, Instructions: cfg.Instructions})
		msgs = st.TaskView(task)
	default:
		return fail(fmt.Errorf("unknown actor %q", actor))
	}
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
