// Command eagent runs a three-actor, event-sourced coding agent.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/harness"
	"github.com/ericflo/eagent/internal/llm"
	"github.com/ericflo/eagent/internal/prompts"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/store"
	"github.com/ericflo/eagent/internal/tools"
	"github.com/ericflo/eagent/internal/ui"
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
  eagent doctor [--live]             check configuration and credentials
  eagent config [--write]            print (or save) the effective configuration
  eagent prompts [export|show NAME]  list, export, or print the actor prompts
  eagent version

Flags (before positional arguments):
  -p            non-interactive: exit when idle (code 0 done, 2 awaiting input)
  -c            resume the latest session
  -v            verbose: show every tool call and model response
  --json        emit narrator output as JSON lines on stdout
  -C <dir>      project directory (default: current directory)
  --preset <n>  model preset: glm (default), astra, anthropic
  --answer <s>  answer the pending question when resuming

Credentials come from the environment: TOGETHER_API_KEY (default models),
OPENAI_API_KEY / OPENROUTER_API_KEY (astra preset), ANTHROPIC_API_KEY.
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
		answer   = fs.String("answer", "", "answer to the pending question")
		showRaw  = fs.Bool("raw", false, "show raw events")
		actor    = fs.String("actor", "", "filter by actor")
		task     = fs.String("task", "", "filter by task id")
		live     = fs.Bool("live", false, "doctor: make a small live call per actor")
		write    = fs.Bool("write", false, "config: write config.json")
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
		case "sessions", "show", "replay", "resume", "doctor", "config", "prompts", "version", "help":
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
		return cmdDoctor(project, *preset, *live)
	case "config":
		return cmdConfig(project, *preset, *write)
	case "prompts":
		return cmdPrompts(project, rest)
	}

	cfg, err := config.Load(project, *preset)
	if err != nil {
		return fail(err)
	}
	opts := harness.Options{Project: project, Interactive: !*batch, Verbose: *verbose, Answer: *answer}
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
	if opts.Prompt == "" && *batch && sessionPath == "" {
		// Allow the prompt on stdin in batch mode.
		if fi, err := os.Stdin.Stat(); err == nil && fi.Mode()&os.ModeCharDevice == 0 {
			raw, _ := readAll(os.Stdin)
			opts.Prompt = strings.TrimSpace(string(raw))
		}
		if opts.Prompt == "" {
			return fail(errors.New("batch mode needs a prompt: eagent -p \"...\""))
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

func cmdDoctor(project, preset string, live bool) int {
	cfg, err := config.Load(project, preset)
	if err != nil {
		return fail(err)
	}
	ok := true
	fmt.Printf("project: %s\n", project)
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
	check := func(name string, a config.Actor) {
		routes, err := a.Routes()
		if err != nil {
			ok = false
			fmt.Printf("  ✗ %-12s %s: %v\n", name, a.Model, err)
			return
		}
		anyLive := false
		for i, ep := range routes {
			label := "  ✓"
			if i > 0 {
				label = "    fallback"
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

func cmdConfig(project, preset string, write bool) int {
	cfg, err := config.Load(project, preset)
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
