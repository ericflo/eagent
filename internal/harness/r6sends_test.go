package harness

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
)

// quietSeat types what it is sent and confirms nothing: what the transcript
// shows is up to the test.
type quietSeat struct {
	*fakeSeat
	qmu   sync.Mutex
	typed []string
	ids   []string
}

func (q *quietSeat) Send(id, text, mode string) (_ string, err error) {
	defer q.sendStart(id)(&err)
	q.fakeSeat.mu.Lock()
	err = q.fakeSeat.sendErr
	q.fakeSeat.mu.Unlock()
	if err != nil {
		return "", err
	}
	q.qmu.Lock()
	q.typed = append(q.typed, text)
	q.ids = append(q.ids, id)
	q.qmu.Unlock()
	return "typed into Codex", nil
}

func (q *quietSeat) typedTexts() []string {
	q.qmu.Lock()
	defer q.qmu.Unlock()
	return append([]string(nil), q.typed...)
}

func (f *fakeSeat) setSendErr(err error) {
	f.mu.Lock()
	f.sendErr = err
	f.mu.Unlock()
}

// userSaysWith records a user message with attachments and passes it on
// the way a message from the phone is; it returns the message's seq.
func userSaysWith(t *testing.T, rt *Runtime, text string, atts ...event.Attachment) int64 {
	t.Helper()
	var seq int64
	rt.sync(func() {
		d := event.UserMessageData{Text: text, Attachments: atts}
		ev := rt.append(event.New(event.UserMessage, event.ActorUser, d))
		seq = ev.Seq
		rel, why := rt.routeUserMessage(d)
		if rel == nil {
			t.Errorf("message not routed to the agent: %s", why)
			return
		}
		rt.forwardUserMessage(rel, ev.Seq, d)
	})
	return seq
}

func forward(rt *Runtime, args map[string]any) (string, bool) {
	return rt.relayTool("relay_forward", argStr(args), argNum(args), argList(args), false)
}

func sendsOf(rt *Runtime, name string) string {
	var out string
	rt.sync(func() { out = strings.Join(userSends(rt.st, name, 8), "\n") })
	return out
}

var plan = event.Attachment{Path: "/w/uploads/plan.pdf", Name: "plan.pdf", ContentType: "application/pdf", Size: 2048}

// A message the terminal refused before anything was typed did not go:
// the list of what the user sent says so, and relay_forward sends it again
// word for word, with its attachments.
func TestAMessageThatCertainlyDidNotGoCanBeForwardedAgainWithItsAttachments(t *testing.T) {
	seat := newFakeSeat("done")
	seat.sendErr = errors.New("the agent is showing something that needs an answer first; check relay_screen and answer it with relay_control keys")
	useGatedLaunches(t, []relaySeat{seat}, nil)
	rt := newLaunchRuntime(t)
	seq := userSaysWith(t, rt, "check the version", plan)
	waitFor(t, "the failure on the record", func() bool { return len(failedDeliveries(rt)) == 1 })
	if s := sendsOf(rt, "codex"); strings.Contains(s, "(arrived)") || !strings.Contains(s, "did not go") {
		t.Errorf("what the user sent, after a send that never went: %q", s)
	}
	seat.setSendErr(nil)
	text, isErr := forward(rt, map[string]any{"seq": seq})
	if isErr {
		t.Fatalf("relay_forward of a message that did not go: %q", text)
	}
	sent := seat.sentTexts()
	if len(sent) != 1 || !strings.HasPrefix(sent[0], "check the version") || !strings.Contains(sent[0], "/w/uploads/plan.pdf") {
		t.Fatalf("typed: %q", sent)
	}
	waitFor(t, "the retry confirmed", func() bool { return strings.Contains(sendsOf(rt, "codex"), "(arrived)") })
	s := sendsOf(rt, "codex")
	if !strings.Contains(s, "did not go") || !strings.Contains(s, "(arrived)") {
		t.Errorf("both sends, truthfully: %q", s)
	}
	if text, isErr := forward(rt, map[string]any{"seq": seq}); !isErr || !strings.Contains(text, "already went to Codex as written") {
		t.Errorf("a third forward after the retry arrived: %q", text)
	}
}

// A message that was typed but never showed up in the transcript may have
// arrived: it is not called arrived, and relay_forward does not send it
// blind, but says what is known and where to look. A record from before
// sends kept their outcome (ok false, nothing else) reads the same way.
func TestATypedButUnconfirmedMessageIsNotCalledArrived(t *testing.T) {
	seat := &quietSeat{fakeSeat: newFakeSeat("done")}
	useGatedLaunches(t, []relaySeat{seat}, nil)
	rt := newLaunchRuntime(t)
	seq := userSaysWith(t, rt, "check the version", plan)
	waitFor(t, "typed", func() bool { return len(seat.typedTexts()) == 1 })
	id := lastSendID(rt)
	seat.emit(relay.Event{Kind: relay.KindDelivered, SendID: id, OK: false, Detail: "never showed up in the agent's transcript; the screen ends:\n› "})
	waitFor(t, "the failure on the record", func() bool { return len(failedDeliveries(rt)) == 1 })
	if s := sendsOf(rt, "codex"); strings.Contains(s, "(arrived)") || !strings.Contains(s, "never confirmed") {
		t.Errorf("what the user sent: %q", s)
	}
	text, isErr := forward(rt, map[string]any{"seq": seq})
	if !isErr || strings.Contains(text, "already went") || !strings.Contains(text, "relay_screen") || !strings.Contains(text, "/w/uploads/plan.pdf") {
		t.Errorf("relay_forward of a message that may have gone: %q", text)
	}
	if n := len(seat.typedTexts()); n != 1 {
		t.Errorf("typed %d times", n)
	}
}
