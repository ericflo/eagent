package harness

import (
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
)

// r7RestartLooks answers looks as the scripted models here do: a message kept for
// eagent gets "Noted.", anything else holds.
func r7RestartLooks(m []map[string]any, prompt int) reply {
	r := hold()
	if strings.Contains(lastUserText(m), "kept for you") && m[len(m)-1]["role"] != "tool" {
		r = say("Noted.")
	}
	r.prompt = prompt
	return r
}

// After the user was told on a 60k model that no restart can make room,
// they pick a 128k model, as the notice advises. The 128k model refuses the
// conversation as it stands (it still holds what the 60k one could not
// take), but one restart on it would fit. That restart is made and the
// message is answered: what was decided on the 60k window is not applied
// to the 128k one.
func TestALargerModelPickedAfterTheFullNoticeGetsItsOwnRestart(t *testing.T) {
	s, _ := r5FullSession(t)
	before := r6Dossiers(s)
	r5Swap(t, s, 128_000, func(_ string, m []map[string]any) reply {
		if ask := dossierRequest(m); ask != "" {
			p := places(ask)
			return reply{text: briefing(p[max(0, len(p)-3):])}
		}
		if raw, _ := json.Marshal(m); strings.Contains(string(raw), fullNotice) {
			// What the 60k conversation holds since its briefing: more
			// than this window takes too, until a restart sets it aside.
			return r7TooLong
		}
		return r7RestartLooks(m, 5000)
	})
	s.rt.sync(func() { s.rt.userMessage(event.UserMessageData{Text: "still there?", Route: "eagent"}) })
	r6Until(t, "an answer, or the user told the message waits", func() bool {
		return r5Told(s, "Noted.") > 0 || r5Told(s, stillFullNotice+`"still there?"`) > 0
	})
	if n := r5Told(s, stillFullNotice+`"still there?"`); n > 0 {
		t.Fatalf("the user was told their message waits (%d restarts on the 128k model), though one restart on it makes room", r6Dossiers(s)-before)
	}
}

// After a normal restart on a 200k model whose first call was 38k, the
// user picks a 60k model. The briefing was sized for 200k; a restart for
// 60k writes one sized for 60k. So the conversation is restarted, and the
// user is not told that no restart can make room.
func TestASmallerModelPickedAfterARestartIsRestartedForItsWindow(t *testing.T) {
	setFor(t, &errorRetryDelays, []time.Duration{time.Millisecond})
	var prompt atomic.Int64
	prompt.Store(120_000)
	s := startSession(t, "", func(_ string, m []map[string]any) reply {
		if ask := dossierRequest(m); ask != "" {
			p := places(ask)
			prompt.Store(38_000)
			// A long briefing, as a 200k model's dossier is.
			return reply{text: briefing(p[max(0, len(p)-3):]) + "\n" + strings.Repeat("The work on the parser went through many steps. ", 2000)}
		}
		return r7RestartLooks(m, int(prompt.Load()))
	}, func(c *config.Config) {
		c.RolloverTokens = 0
		c.Model.ContextTokens, c.Model.MaxTokens = 200_000, 2048
	})
	r5LogOnFailure(s)
	s.settled()
	s.rt.sync(func() { s.rt.userMessage(event.UserMessageData{Text: "first", Route: "eagent"}) })
	r6Until(t, "the first restart", func() bool { return r6Dossiers(s) == 1 })
	s.rt.sync(func() { s.rt.userMessage(event.UserMessageData{Text: "second", Route: "eagent"}) })
	r6Until(t, "the first call after the restart", func() bool {
		fp := 0
		s.rt.sync(func() { fp = int(s.rt.turns.firstPrompt) })
		return fp == 38_000 && r5Told(s, "Noted.") >= 2
	})
	r5Swap(t, s, 60_000, func(_ string, m []map[string]any) reply {
		if ask := dossierRequest(m); ask != "" {
			p := places(ask)
			return reply{text: briefing(p[max(0, len(p)-3):])}
		}
		return r7RestartLooks(m, 5000)
	})
	s.rt.sync(func() { s.rt.userMessage(event.UserMessageData{Text: "third", Route: "eagent"}) })
	r6Until(t, "a restart on the 60k model, or the full notice", func() bool {
		return r6Dossiers(s) >= 2 || r6ToldFull(s) > 0
	})
	if n := r6ToldFull(s); n > 0 {
		t.Fatalf("told %d times that no restart can make room, right after a switch to a smaller window", n)
	}
}

// r7TooLong is a provider's refusal of a request as too long.
var r7TooLong = reply{status: 400, body: `{"error":{"message":"prompt is too long for the model's context window","type":"invalid_request_error"}}`}

// A model whose first call of every conversation goes through but is sent
// back (plain text, no tool call), and whose second call, the reply and the
// correction appended, is refused as too long. Each conversation is
// "refused after its calls went through", so each restart once reset the
// count of restarts in a row and carried the same unseen message whole into
// the next: a paid dossier pass after another, without end. The restarts in
// a row are bounded, and the user is told.
func TestARefusalAfterASentBackCallDoesNotRestartWithoutEnd(t *testing.T) {
	setFor(t, &errorRetryDelays, []time.Duration{time.Millisecond})
	brain := func(_ string, m []map[string]any) reply {
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
	})
	r5LogOnFailure(s)
	s.settled()
	s.rt.sync(func() { s.rt.userMessage(event.UserMessageData{Text: "what is the status?", Route: "eagent"}) })
	r6Until(t, "the user told the conversation is full, or too many restarts", func() bool {
		return r6ToldFull(s) > 0 || r6Dossiers(s) > restartRescueMax+3
	})
	if n := r6Dossiers(s); n > restartRescueMax+2 {
		t.Fatalf("%d restarts in a row, each after a refusal on the second call; want at most %d", n, restartRescueMax+2)
	}
	if n := r5Told(s, fullNotice+": after several restarts in a row"); n != 1 {
		t.Fatalf("told %d times that the restarts stopped as the window was reached again, want once", n)
	}
}

// After restarts in a row stopped on a 60k model, the user picks a 128k
// model, as the notice advises. It takes the conversation and answers.
// When the conversation later fills that window it is restarted: the
// restarts counted on the old model do not stop it (an independent read's
// case; the session's end also checks the first call's size against the
// record).
func TestALargerModelThatFillsLaterIsRestartedForItsWindow(t *testing.T) {
	setFor(t, &errorRetryDelays, []time.Duration{time.Millisecond})
	brain := func(_ string, m []map[string]any) reply {
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
	var full, restarted atomic.Bool
	r5Swap(t, s, 128_000, func(_ string, m []map[string]any) reply {
		if ask := dossierRequest(m); ask != "" {
			restarted.Store(true)
			p := places(ask)
			return reply{text: briefing(p[max(0, len(p)-3):])}
		}
		if full.Load() && !restarted.Load() {
			return r7TooLong
		}
		return r7RestartLooks(m, 5000)
	})
	s.rt.sync(func() { s.rt.userMessage(event.UserMessageData{Text: "still there?", Route: "eagent"}) })
	r6Until(t, "the 128k model answers", func() bool { return r5Told(s, "Noted.") > 0 })
	before := r6Dossiers(s)
	full.Store(true)
	s.rt.sync(func() { s.rt.userMessage(event.UserMessageData{Text: "and now?", Route: "eagent"}) })
	r6Until(t, "a restart on 128k, or the user told the message waits", func() bool {
		return r6Dossiers(s) > before || r5Told(s, stillFullNotice+`"and now?"`) > 0
	})
}

// The same pick, and the 128k model refuses the conversation as it stands:
// one restart on it makes room, and that restart is made.
func TestALargerModelPickedAfterRestartsStoppedGetsItsOwn(t *testing.T) {
	setFor(t, &errorRetryDelays, []time.Duration{time.Millisecond})
	brain := func(_ string, m []map[string]any) reply {
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
	var restarted atomic.Bool
	var refused atomic.Int64
	r5Swap(t, s, 128_000, func(_ string, m []map[string]any) reply {
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
	s.rt.sync(func() { s.rt.userMessage(event.UserMessageData{Text: "still there?", Route: "eagent"}) })
	r6Until(t, "an answer, or the user told the message waits", func() bool {
		return r5Told(s, "Noted.") > 0 || r5Told(s, stillFullNotice+`"still there?"`) > 0
	})
	if n := r5Told(s, stillFullNotice+`"still there?"`); n > 0 {
		t.Fatalf("on the 128k model: refused %d times, %d restarts; the user was told the message waits, though one restart makes room", refused.Load(), r6Dossiers(s)-before)
	}
}
