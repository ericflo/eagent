package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/llm"
)

func TestParseInterleaved(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	raw := fs.Bool("raw", false, "")
	answer := fs.String("answer", "", "")
	rest, err := parseInterleaved(fs, []string{"show", "1788", "--raw"})
	if err != nil || !*raw || strings.Join(rest, " ") != "show 1788" {
		t.Fatalf("rest=%v raw=%v err=%v", rest, *raw, err)
	}
	fs2 := flag.NewFlagSet("t", flag.ContinueOnError)
	answer = fs2.String("answer", "", "")
	rest, err = parseInterleaved(fs2, []string{"resume", "1788", "--answer", "yes", "more", "words"})
	if err != nil || *answer != "yes" || strings.Join(rest, " ") != "resume 1788 more words" {
		t.Fatalf("rest=%v answer=%q err=%v", rest, *answer, err)
	}
	fs3 := flag.NewFlagSet("t", flag.ContinueOnError)
	fs3.Bool("p", false, "")
	rest, _ = parseInterleaved(fs3, []string{"-p", "--", "--not-a-flag", "prompt"})
	if strings.Join(rest, " ") != "--not-a-flag prompt" {
		t.Fatalf("rest=%v", rest)
	}
}

// Words after "--" are a prompt: never flags, never a command.
func TestArgsAfterTerminatorAreWords(t *testing.T) {
	for _, c := range []struct {
		args     []string
		rest     string
		commands int
	}{
		{[]string{"-C", "x", "-p", "--", "help"}, "help", 0},
		{[]string{"-p", "--", "- fix login"}, "- fix login", 0},
		{[]string{"-p", "--", "-v"}, "-v", 0},
		{[]string{"--", "doctor", "-p"}, "doctor -p", 0},
		{[]string{"doctor"}, "doctor", 1},
		{[]string{"-p", "resume", "1788", "--", "-go on"}, "resume 1788 -go on", 2},
	} {
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		fs.Bool("p", false, "")
		verbose := fs.Bool("v", false, "")
		fs.String("C", "", "")
		rest, commands, err := parseArgs(fs, c.args)
		if err != nil || strings.Join(rest, " ") != c.rest || commands != c.commands || *verbose {
			t.Errorf("%v: rest=%q commands=%d verbose=%v err=%v", c.args, rest, commands, *verbose, err)
		}
	}
}

func TestCmdServersLifecycle(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/models":
			fmt.Fprint(w, `{"object":"list","data":[{"id":"cli-test-model","context_length":65536}]}`)
		case "/v1/chat/completions", "/v1/responses":
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":"missing field"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer mock.Close()

	project := t.TempDir()
	if code := cmdServers(project, []string{"probe", mock.URL}, serverCLIOpts{}); code != 0 {
		t.Fatalf("probe exit code = %d", code)
	}
	if code := cmdServers(project, []string{"add", mock.URL}, serverCLIOpts{id: "cli-box", label: "CLI Box"}); code != 0 {
		t.Fatalf("add exit code = %d", code)
	}
	if code := cmdServers(project, []string{"list"}, serverCLIOpts{}); code != 0 {
		t.Fatalf("list exit code = %d", code)
	}
	if code := cmdServers(project, []string{"use", "cli-box", "cli-test-model"}, serverCLIOpts{protocol: llm.ProtocolResponses}); code != 0 {
		t.Fatalf("use exit code = %d", code)
	}
	cfg, err := config.Load(project, "")
	if err != nil {
		t.Fatalf("Load after use: %v", err)
	}
	if cfg.Model.BaseURL != mock.URL+"/v1" || cfg.Model.Model != "cli-test-model" || cfg.Model.Protocol != llm.ProtocolResponses || cfg.Model.APIKeyEnv != "" {
		t.Fatalf("cfg.Model = %+v", cfg.Model)
	}
	if cfg.Model.ContextTokens != 65536 {
		t.Fatalf("context_tokens = %d, want the picked model's", cfg.Model.ContextTokens)
	}
	// A model the server has not listed: its window is unknown, not the
	// one of the model before.
	if code := cmdServers(project, []string{"use", "cli-box", "not-listed-model"}, serverCLIOpts{}); code != 0 {
		t.Fatalf("use exit code = %d", code)
	}
	if cfg, err = config.Load(project, ""); err != nil || cfg.Model.Model != "not-listed-model" || cfg.Model.ContextTokens != 0 {
		t.Fatalf("after use of an unlisted model: %+v %v", cfg.Model, err)
	}
	// Chat Completions reads replay_reasoning too: a switch to it keeps the
	// setting.
	raw, err := os.ReadFile(config.File(project))
	if err != nil {
		t.Fatal(err)
	}
	var file map[string]map[string]any
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	file["model"]["replay_reasoning"] = false
	raw, _ = json.Marshal(file)
	if err := os.WriteFile(config.File(project), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if code := cmdServers(project, []string{"use", "cli-box", "cli-test-model"}, serverCLIOpts{protocol: llm.ProtocolChat}); code != 0 {
		t.Fatalf("use exit code = %d", code)
	}
	if cfg, err = config.Load(project, ""); err != nil || cfg.Model.Protocol != llm.ProtocolChat || cfg.Model.ReplayReasoning == nil || *cfg.Model.ReplayReasoning {
		t.Fatalf("after a switch to Chat Completions: %+v %v", cfg.Model, err)
	}
	if code := cmdServers(project, []string{"refresh", "cli-box"}, serverCLIOpts{}); code != 0 {
		t.Fatalf("refresh exit code = %d", code)
	}
	if code := cmdServers(project, []string{"remove", "cli-box"}, serverCLIOpts{}); code != 0 {
		t.Fatalf("remove exit code = %d", code)
	}
	if servers, _ := config.LoadServers(project); len(servers) != 0 {
		t.Fatalf("servers after remove = %+v", servers)
	}
}

// `eagent view --actor X` (and --task) are accepted and said to be ignored.
func TestViewIgnoredFlags(t *testing.T) {
	var b strings.Builder
	viewIgnoredFlags(&b, "", "")
	if b.String() != "" {
		t.Fatalf("no flags: %q", b.String())
	}
	viewIgnoredFlags(&b, "narrator", "")
	if b.String() != "eagent view: there is one conversation now, so --actor is ignored\n" {
		t.Fatalf("--actor: %q", b.String())
	}
	b.Reset()
	viewIgnoredFlags(&b, "orchestrator", "t3")
	if b.String() != "eagent view: there is one conversation now, so --actor and --task are ignored\n" {
		t.Fatalf("both: %q", b.String())
	}
	// Both flags still parse rather than failing with "flag provided but not
	// defined".
	if code := run([]string{"view", "--actor", "narrator", "--task", "t1", "-C", t.TempDir()}); code == 64 {
		t.Fatal("view --actor/--task was a usage error")
	}
}

// A batch run that names an agent may start without a prompt (as on main).
func TestBatchNeedsPrompt(t *testing.T) {
	if !batchNeedsPrompt("", "") || batchNeedsPrompt("", "claude") || batchNeedsPrompt("fix it", "") {
		t.Fatal("batchNeedsPrompt")
	}
}

// The takeover of an older session by the one-loop eagent shows in
// `eagent show`, like a rollover does.
func TestTranscriptLineSubsessions(t *testing.T) {
	for reason, want := range map[string]string{
		"rollover": "subsession   3 (s2.jsonl) after context rollover",
		"upgrade":  "subsession   3 (s2.jsonl) continued by the one-loop eagent from a briefing",
	} {
		ev := event.New(event.SubsessionStart, event.ActorHarness, event.SubsessionStartData{File: "s2.jsonl", Index: 2, Reason: reason})
		if got := transcriptLine(ev); !strings.HasSuffix(got, want) {
			t.Errorf("%s: %q", reason, got)
		}
	}
	if got := transcriptLine(event.New(event.SubsessionStart, event.ActorHarness, event.SubsessionStartData{File: "s0.jsonl", Reason: "new"})); got != "" {
		t.Errorf("new: %q", got)
	}
}
