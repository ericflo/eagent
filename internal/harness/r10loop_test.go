package harness

import (
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/llm"
)

// When the restart a refusal called for could not be made (the file for
// the next conversation could not be created), the conversation in use was
// never restarted: the user is not told it is refused "even after I
// restarted it from my notes", but what any failed call tells them.
func TestRefusedAfterAFailedRestartIsNotCalledRestarted(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("a directory cannot be made read-only for root")
	}
	gate := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	var tooLong, writing atomic.Bool
	s := startSession(t, brief, func(_ string, msgs []map[string]any) reply {
		if ask := dossierRequest(msgs); ask != "" {
			writing.Store(true)
			p := places(ask)
			return reply{block: gate, text: briefing(p[:min(len(p), 4)])}
		}
		if tooLong.Load() {
			return reply{status: 400, body: tooLongBody}
		}
		return hold()
	}, nil)
	t.Cleanup(release)
	s.waitFor("the agent's turn", func() bool { return len(s.events(event.RelayTurn)) == 1 })
	s.settled()
	tooLong.Store(true)
	s.type_("@e are you there?")
	s.waitFor("the dossier call to go out", writing.Load)
	dir := s.rt.sess.Path
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	release()
	told := func() string {
		for _, m := range s.ui.said() {
			if strings.Contains(m, "refuses my conversation") || strings.Contains(m, "can't reach my own model") {
				return m
			}
		}
		return ""
	}
	s.waitFor("the user to be told about the failing calls", func() bool { return told() != "" })
	if n := len(s.events(event.SubsessionStart)); n != 1 {
		t.Fatalf("%d conversations begun; the restart was meant to fail", n)
	}
	if m := told(); strings.Contains(m, "restarted it from my notes") {
		t.Fatalf("the user was told of a restart that was never made: %q", m)
	}
}

// After restarts in a row stopped on a 60k model, that model's provider
// stops serving it and the session falls back to the 128k model of its
// chain. As for a 128k model picked in settings, the 128k one refuses the
// conversation as it stands, one restart on it makes room, and that
// restart is made: what the 60k model needed is not counted against it.
func TestAFallbackWithALargerWindowGetsItsOwnRestart(t *testing.T) {
	setFor(t, &errorRetryDelays, []time.Duration{time.Millisecond})
	var restarted, gone atomic.Bool
	var refused atomic.Int64
	fallback := newScripted(func(_ string, m []map[string]any) reply {
		if ask := dossierRequest(m); ask != "" {
			restarted.Store(true)
			p := places(ask)
			return reply{text: briefing(p[max(0, len(p)-3):])}
		}
		if !restarted.Load() {
			refused.Add(1)
			return r7TooLong
		}
		return r7RestartLooks(m, 5000)
	})
	t.Cleanup(fallback.srv.Close)
	brain := func(_ string, m []map[string]any) reply {
		if gone.Load() {
			return reply{status: 401, body: `{"error":{"message":"invalid api key","type":"authentication_error"}}`}
		}
		if ask := dossierRequest(m); ask != "" {
			p := places(ask)
			return reply{text: briefing(p[max(0, len(p)-3):])}
		}
		if strings.HasPrefix(lastUserText(m), "[eagent] That reply had no tool call") {
			return r7TooLong
		}
		return reply{text: "let me think about that"}
	}
	s := startSession(t, "", brain, func(c *config.Config) {
		c.RolloverTokens = 0
		c.Model.ContextTokens, c.Model.MaxTokens = 60000, 2048
		c.Model.Fallback = &config.ModelRoute{Protocol: llm.ProtocolChat, BaseURL: fallback.srv.URL, Model: "other-model", APIKeyEnv: "EAGENT_TEST_KEY", ContextTokens: 128_000, MaxTokens: 2048}
	})
	r5LogOnFailure(s)
	s.settled()
	s.rt.sync(func() { s.rt.userMessage(event.UserMessageData{Text: "what is the status?", Route: "eagent"}) })
	r6Until(t, "the fullGrew notice", func() bool {
		return r5Told(s, fullNotice+": after several restarts in a row") > 0 || r6Dossiers(s) > restartRescueMax+3
	})
	if r5Told(s, fullNotice+": after several restarts in a row") == 0 {
		t.Fatalf("no fullGrew notice")
	}
	before := r6Dossiers(s)
	gone.Store(true)
	s.rt.sync(func() { s.rt.userMessage(event.UserMessageData{Text: "still there?", Route: "eagent"}) })
	deadline := time.Now().Add(5 * time.Second)
	for r5Told(s, "Noted.") == 0 && r5Told(s, stillFullNotice+`"still there?"`) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if r5Told(s, "Noted.") == 0 {
		t.Fatalf("on the 128k fallback (asked %d times): refused %d times, %d restarts, told the message waits %d times; the message is unanswered, though one restart makes room",
			fallback.count("other-model"), refused.Load(), r6Dossiers(s)-before, r5Told(s, stillFullNotice))
	}
}
