package harness

import (
	"context"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
)

// k26UI is fakeUI with a hook on Trace, which the loop calls for every
// event it records.
type k26UI struct {
	*fakeUI
	onTrace func(event.Event)
}

func (u *k26UI) Trace(ev event.Event) {
	if u.onTrace != nil {
		u.onTrace(ev)
	}
}

// A closing turn whose first call looked at something (a call that wants
// another) is taken over by the user's message, which came after the
// turn's check that the session is still ending and before its next call:
// that call is made for the user's message (dueWake), and its reply answers
// it. The user writes again while that reply is out. The answer goes out,
// as an ordinary turn's does, and without the closing word's buzz: the
// session stopped ending. (A check on closing words refused it as one the
// user had overtaken.)
func TestK26AClosingTurnTakenOverByTheUsersMessageSendsItsAnswer(t *testing.T) {
	const answer = "Answer one: yes, #42 is merged."
	var phase atomic.Int32
	var armed atomic.Bool
	var rtp atomic.Pointer[Runtime]
	ui := &k26UI{fakeUI: &fakeUI{input: make(chan string, 16)}}
	ui.onTrace = func(ev event.Event) {
		// The loop goroutine, recording the closing call's tool result. The
		// loop is held until the turn's check that the session is still
		// ending is queued; the user's message goes right behind it, so it
		// is recorded after that check and before the next call's step.
		if ev.Type != event.ToolResult || !armed.CompareAndSwap(true, false) {
			return
		}
		r := rtp.Load()
		gate, started := make(chan struct{}), make(chan struct{})
		r.post(func() { close(started); <-gate })
		go func() {
			<-started
			for len(r.loop) == 0 { // the turn's stillClosing check is queued
				runtime.Gosched()
			}
			r.post(func() { r.userMessage(event.UserMessageData{Text: "first question: is #42 merged?"}) })
			close(gate)
		}()
	}
	model := audit2Model(t, func(_ string, msgs []map[string]any) reply {
		tail, all := lastUserText(msgs), allText(msgs)
		switch {
		case phase.Load() == 2 && strings.Contains(all, "first question") && msgs[len(msgs)-1]["role"] != "tool" && phase.CompareAndSwap(2, 3):
			// The call made for the user's message. They write again
			// while it is out.
			r12Write(&rtp, "second question: and the docs?")
			return say(answer)
		case msgs[len(msgs)-1]["role"] == "tool":
			return hold()
		case strings.Contains(tail, "The session is ending now") && phase.CompareAndSwap(1, 2):
			armed.Store(true)
			return reply{calls: []event.ToolCall{tc("view_image", `{"path":"/nonexistent/sr15.png"}`)}}
		case strings.Contains(tail, "finished a turn") && phase.CompareAndSwap(0, 1):
			return stopsWhenDone()
		case phase.Load() >= 3 && strings.Contains(all, "second question") && phase.CompareAndSwap(3, 4):
			return say("Answer two: the docs are updated.")
		}
		return hold()
	})
	seat := newFakeSeat("Merged #42; the tests are green.")
	t.Setenv("EAGENT_TEST_KEY", "x")
	useFakeRelays(t, seat)
	rt, err := New(testConfig(model.srv.URL), Options{Prompt: brief, Project: t.TempDir()}, ui)
	if err != nil {
		t.Fatal(err)
	}
	rtp.Store(rt)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	s := &session{t: t, rt: rt, ui: ui.fakeUI, seat: seat, model: model, cancel: cancel, done: make(chan int, 1)}
	go func() { s.done <- rt.Run(ctx) }()
	t.Cleanup(s.stop)
	code := coreExit(s)
	if phase.Load() < 3 {
		t.Fatalf("setup: the closing turn was not taken over by the user's message (phase %d); said %q", phase.Load(), s.ui.said())
	}
	refused, sent, buzzed := "", false, false
	for _, ev := range mustReadCore(t, s.rt.sess.Path) {
		var d event.ToolResultData
		if ev.Type == event.ToolResult && ev.Decode(&d) == nil && strings.HasPrefix(d.Output, "not sent") {
			refused = d.Output
		}
		var m event.MessageData
		if ev.Type == event.Message && ev.Decode(&m) == nil && m.Text == answer {
			sent, buzzed = true, m.Important
		}
	}
	if !sent || refused != "" {
		t.Fatalf("exit %d: the answer to the user's first message, made in a call made for it, was never sent; the model was told %q; said %q", code, refused, s.ui.said())
	}
	if buzzed {
		t.Fatalf("exit %d: the answer to the user's message went out marked important, as a closing word is, though the session had stopped ending", code)
	}
}
