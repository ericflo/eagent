package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
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
