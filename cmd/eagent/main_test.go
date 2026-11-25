package main

import (
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/config"
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
