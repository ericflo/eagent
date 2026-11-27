package harness

import (
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
)

// A model on a server registered in the project, whose window only the
// project's live list knows (the settings editor saved its route with
// context_tokens 0 before the server was probed). Its window is the one
// the list gives, as everywhere else a route's window is looked up
// (config.KnownContext), and not the 200,000 assumed for a model nobody
// knows: the restart size follows it.
func TestR13WindowOfARegisteredServersModelComesFromTheLiveList(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	useFakeRelays(t, newFakeSeat("ok"))
	project := t.TempDir()
	const url = "http://127.0.0.1:8099/v1"
	if err := os.MkdirAll(filepath.Dir(config.ServersPath(project)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.ServersPath(project), []byte(`{"servers":[{"id":"local-loom","label":"Loom","base_url":"`+url+`","protocol":"openai-chat"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteLiveCache(project, &config.LiveCache{FetchedAt: time.Now(), Models: []config.Model{{Provider: "local-loom", ID: testModel, Label: "Local", Context: 32768, Live: true}}}); err != nil {
		t.Fatal(err)
	}
	if got := config.KnownContext(project, url, testModel); got != 32768 {
		t.Fatalf("config.KnownContext = %d, want 32768 (the test's setup is wrong)", got)
	}
	cfg := testConfig(url)
	cfg.RolloverTokens = 0
	cfg.Model.ContextTokens, cfg.Model.MaxTokens = 0, 0
	rt, err := New(cfg, Options{Project: project}, &fakeUI{input: make(chan string)})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.sess.Close()
	if window, _ := rt.window(); window != 32768 {
		t.Fatalf("the route's window is %d, want 32768, the live list's", window)
	}
	_, fixed := rt.inputRoom()
	if got, want := int(rt.rolloverTokens()), config.AutoRolloverWithin(32768, 0, int(fixed)); got != want {
		t.Fatalf("restarts at %d, want %d (a 32,768-token window's, inside its input room)", got, want)
	}
}

// The provider refuses even the opening of a conversation a restart began
// (the notes and the standing instructions), with rollover_tokens unset.
// No call goes through, so the user is not told that eagent carries on,
// nor to raise a restart size that is not what was refused: they are told
// the model refuses it, and what would take it.
func TestR13AnOpeningTheModelRefusesIsNotToldToCarryOn(t *testing.T) {
	s, _ := r5FullSession(t)
	var full []string
	for _, m := range s.ui.said() {
		if strings.HasPrefix(m, fullNotice) {
			full = append(full, m)
		}
	}
	if len(full) != 1 {
		t.Fatalf("told %d times that the conversation is full, want once: %q", len(full), s.ui.said())
	}
	if m := full[0]; strings.Contains(m, "carry on") || strings.Contains(m, "rollover_tokens") || !strings.Contains(m, "refuses") || !strings.Contains(m, "larger context") {
		t.Fatalf("on a refusal of the opening alone the user was told %q; want that the model refuses it and that a model with a larger context would take it, with no carrying on and no rollover_tokens", m)
	}
	for _, l := range s.ui.logged() {
		if strings.Contains(l, "restarting again cannot shrink it") && strings.Contains(l, "rollover_tokens") {
			t.Fatalf("the log says %q on a refusal of the opening alone", l)
		}
	}
}

// A working agent records progress while every dossier is written, and
// every conversation's first turn reads it, makes a lookup and has the
// call after the lookup refused as too long. Reading the progress is not
// growth: the turn never ended, and the same thing happens after every
// restart. Each of those restarts counts in the row, so they stop at the
// bound and the user is told, where each once started the count again: a
// paid dossier pass after another, without end.
func TestR13ProgressReadInAFirstTurnThatOverflowsDoesNotRestartWithoutEnd(t *testing.T) {
	setFor(t, &errorRetryDelays, []time.Duration{time.Millisecond})
	var s *session
	var ready atomic.Bool
	brain := func(_ string, m []map[string]any) reply {
		if ask := dossierRequest(m); ask != "" {
			if ready.Load() {
				s.rt.sync(func() {
					s.rt.append(event.New(event.RelayProgress, event.ActorHarness, event.RelayProgressData{Name: "codex", Text: "running the tests"}))
				})
			}
			p := places(ask)
			return reply{text: briefing(p[max(0, len(p)-3):])}
		}
		switch {
		case !ready.Load():
			return hold()
		case m[len(m)-1]["role"] == "tool":
			return r7TooLong
		}
		return reply{calls: []event.ToolCall{tc("session_search", `{"query":"tests"}`)}}
	}
	s = startSession(t, "", brain, func(c *config.Config) {
		c.RolloverTokens = 0
		c.Model.ContextTokens, c.Model.MaxTokens = 60000, 2048
	})
	r5LogOnFailure(s)
	s.settled()
	ready.Store(true)
	s.rt.sync(func() { s.rt.userMessage(event.UserMessageData{Text: "what is the status?", Route: "eagent"}) })
	r6Until(t, "the user told the conversation is full, or too many restarts", func() bool {
		return r6ToldFull(s) > 0 || r6Dossiers(s) > restartRescueMax+4
	})
	if n := r6Dossiers(s); n > restartRescueMax+2 {
		t.Fatalf("%d restarts in a row, each after a first turn that read the progress and was refused after its lookup; want at most %d", n, restartRescueMax+2)
	}
	if n := r5Told(s, fullNotice+": after several restarts in a row"); n != 1 {
		t.Fatalf("told %d times that the restarts stopped as the window was reached again, want once", n)
	}
}
