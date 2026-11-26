package harness

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
)

// r5PassSession starts a session whose first look asks the user a question
// and sets a timer, with a prompt past the restart size, so a dossier pass
// begins right after it. The pass waits for release. after is the first
// request made once the pass is over.
type r5PassSession struct {
	*session
	release  chan struct{}
	once     sync.Once
	mu       sync.Mutex
	asked    int
	after    []map[string]any
	answered atomic.Int32
}

func (p *r5PassSession) open() { p.once.Do(func() { close(p.release) }) }

func r5StartPass(t *testing.T) *r5PassSession {
	p := &r5PassSession{release: make(chan struct{})}
	var looks atomic.Int32
	p.session = startSession(t, brief, func(_ string, msgs []map[string]any) reply {
		if ask := dossierRequest(msgs); ask != "" {
			p.mu.Lock()
			p.asked++
			p.mu.Unlock()
			<-p.release
			return reply{text: briefing(places(ask)[:3])}
		}
		if looks.Add(1) == 1 {
			r := reply{calls: []event.ToolCall{
				tc("schedule", `{"spec":"in 100ms","note":"Tell Codex to deploy to staging."}`),
				tc("ask_user", `{"text":"Merge it?","options":["yes","no"]}`),
			}}
			r.prompt = 25000
			return r
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.after == nil {
			p.after = msgs
		}
		p.answered.Add(1)
		return say("Done.")
	}, nil)
	t.Cleanup(p.open)
	p.waitFor("the dossier pass", func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.asked == 1
	})
	return p
}

// rereadNotices counts the times the user was told the session is being
// reread.
func (p *r5PassSession) rereadNotices() int {
	return r5Told(p.session, rereadNotice)
}

// While the conversation is restarted from a dossier, eagent makes no other
// model call. What arrives meanwhile is recorded as it comes and kept: a
// timer coming due, the user's answer to a question, an agent stopping on
// a dialog, a message for eagent. The first call after the pass has each
// of them in the conversation, oldest first, and its last message names
// every one. The user, who wrote and would otherwise hear nothing for
// minutes, is told once, at once, that the session is being reread.
func TestWhatArrivesDuringADossierPassIsKeptAndTakenUpAfterIt(t *testing.T) {
	p := r5StartPass(t)
	p.waitFor("the timer to come due", func() bool { return len(p.events(event.ScheduleFire)) == 1 })
	if n := p.rereadNotices(); n != 0 {
		t.Fatalf("the user was told %d times that the session is being reread, and they have not written", n)
	}
	p.type_("yes")
	p.waitFor("the answer on the record", func() bool { return len(p.events(event.UserAnswer)) == 1 })
	p.waitFor("the user told the session is being reread", func() bool { return p.rereadNotices() == 1 })
	p.type_("@e how is it going?")
	p.seat.emit(relay.Event{Kind: relay.KindState, State: "blocked", Detail: "Do you trust this folder? 1. Yes 2. No"})
	p.waitFor("the message and the dialog on the record", func() bool {
		return len(p.events(event.UserMessage)) == 2 && len(p.events(event.RelayState)) > 0
	})
	p.settledWhileBusy()
	if n := p.rereadNotices(); n != 1 {
		t.Fatalf("the user was told %d times that the session is being reread, want once for the pass", n)
	}
	if n := p.answered.Load(); n != 0 {
		t.Fatalf("%d model calls were made during the pass", n)
	}
	p.open()
	p.waitFor("the first call after the pass", func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.after != nil
	})
	p.mu.Lock()
	after := p.after
	p.mu.Unlock()
	text := allText(after[:len(after)-1])
	order := []string{"Tell Codex to deploy to staging.", "yes", rereadNotice, "how is it going?", "Do you trust this folder?"}
	at := -1
	for _, want := range order {
		i := strings.Index(text[at+1:], want)
		if i < 0 {
			t.Fatalf("the conversation after the pass does not have %q after what came before it:\n%s", want, text)
		}
		at += 1 + i
	}
	tail := lastUserText(after)
	for _, want := range []string{"answered your question", "kept for you", "stuck on something on its screen", "A timer you set came due"} {
		if !strings.Contains(tail, want) {
			t.Fatalf("the first call after the pass does not name %q:\n%s", want, tail)
		}
	}
	p.waitFor("the answer", func() bool { return r5Told(p.session, "Done.") > 0 })
	if n := p.rereadNotices(); n != 1 {
		t.Fatalf("the user was told %d times that the session is being reread, want once", n)
	}
}

// A message for eagent that came while the call before the pass was out,
// and that no look has taken in, waits as long as one written during the
// pass: its writer is told as the pass begins.
func TestTheUserWhoseMessageWaitsOnAPassIsToldAsItBegins(t *testing.T) {
	inFirst := make(chan struct{})
	release, go1 := make(chan struct{}), make(chan struct{})
	var once, once1 sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }); once1.Do(func() { close(go1) }) })
	var looks atomic.Int32
	s := startSession(t, brief, func(_ string, msgs []map[string]any) reply {
		if dossierRequest(msgs) != "" {
			<-release
			return reply{text: briefing(places(dossierRequest(msgs))[:3])}
		}
		if looks.Add(1) == 1 {
			close(inFirst)
			<-go1
			return reply{calls: []event.ToolCall{tc("hold", `{}`)}, prompt: 25000}
		}
		return say("Done.")
	}, nil)
	<-inFirst
	s.type_("@e how is it going?")
	s.waitFor("the message on the record", func() bool { return len(s.events(event.UserMessage)) == 2 })
	once1.Do(func() { close(go1) })
	s.waitFor("the user told the session is being reread", func() bool { return r5Told(s, rereadNotice) == 1 })
	compacting := false
	s.rt.sync(func() { compacting = s.rt.turns.compacting })
	if !compacting {
		t.Fatal("the user was told the session is being reread, and no pass is running")
	}
	once.Do(func() { close(release) })
	s.waitFor("the answer", func() bool { return r5Told(s, "Done.") > 0 })
	if n := r5Told(s, rereadNotice); n != 1 {
		t.Fatalf("the user was told %d times, want once", n)
	}
}

// A message typed straight into the agent's terminal is not waiting on
// eagent: a pass says nothing about it.
func TestAMessageForTheAgentDuringAPassSaysNothing(t *testing.T) {
	p := r5StartPass(t)
	p.rt.sync(func() { p.rt.dismissQuestion("", "") })
	p.type_("please also run the linter")
	p.waitFor("the message on the record", func() bool { return len(p.events(event.UserMessage)) == 2 })
	p.settledWhileBusy()
	if d := p.events(event.UserMessage)[1]; strings.Contains(string(d.Data), `"held"`) {
		t.Fatalf("the message was kept for eagent: %s", d.Data)
	}
	if n := p.rereadNotices(); n != 0 {
		t.Fatalf("the user was told %d times that the session is being reread, for a message typed into the agent", n)
	}
}

// settledWhileBusy lets the loop run its scheduler a few times: a pass
// keeps it busy, so session.settled would never return.
func (p *r5PassSession) settledWhileBusy() {
	for i := 0; i < 8; i++ {
		p.rt.sync(func() {})
	}
}
