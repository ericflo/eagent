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

// sr13UI is fakeUI with a hook on Stream, which the turn's goroutine calls
// right after each model call comes back.
type sr13UI struct {
	*fakeUI
	onStream func(kind, delta string)
}

func (u *sr13UI) Stream(kind, delta string) {
	if u.onStream != nil {
		u.onStream(kind, delta)
	}
}

// The closing call answers with send_message; the user wrote while it was
// out, so the session is not ending. What the model tells the user is
// never held back: the report goes out, without the buzz of a closing word
// (the session is not saying its last), and the user's question is
// answered next. (For two passes a check refused such a message; each time
// it also refused one that should have gone.)
func TestSR13SendMessageClosingReportAfterTheUserWrote(t *testing.T) {
	const report = "Final report: #42 is merged and this session is ending."
	const answer = "Yes, #42 is merged and the tests are green."
	var phase atomic.Int32
	s, _ := r12Start(t, newFakeSeat("Merged #42; the tests are green."), func(rt *atomic.Pointer[Runtime], msgs []map[string]any) reply {
		tail := lastUserText(msgs)
		switch {
		case strings.Contains(tail, "The session is ending now") && phase.CompareAndSwap(1, 2):
			r12Write(rt, "is #42 merged?")
			return say(report)
		case phase.CompareAndSwap(2, 3):
			return say(answer)
		case strings.Contains(tail, "finished a turn") && phase.CompareAndSwap(0, 1):
			return stopsWhenDone()
		}
		return hold()
	})
	code := coreExit(s)
	if phase.Load() < 2 {
		t.Fatalf("setup: no closing call (phase %d)", phase.Load())
	}
	var asked, reported, answered int64
	buzzed := false
	for _, ev := range mustReadCore(t, s.rt.sess.Path) {
		var u event.UserMessageData
		if ev.Type == event.UserMessage && ev.Decode(&u) == nil && u.Text == "is #42 merged?" {
			asked = ev.Seq
		}
		var m event.MessageData
		if ev.Type == event.Message && ev.Decode(&m) == nil {
			switch m.Text {
			case report:
				reported, buzzed = ev.Seq, m.Important
			case answer:
				answered = ev.Seq
			}
		}
	}
	if asked == 0 || reported < asked || answered < reported {
		t.Fatalf("exit %d: the user's question at %d, the report at %d, the answer at %d; want the report sent and the question answered after it; said %q", code, asked, reported, answered, s.ui.said())
	}
	if buzzed {
		t.Fatalf("exit %d: the report went out marked important, as a closing word is, though the user had written and the session was not ending", code)
	}
}

// The plain-text report path asks stillClosing and records the text as the
// important closing word in one step of the loop. (They were two: a message
// from the user that the loop took between them, the phone's post queued
// behind the check, still got the report.) Here the loop is held while the
// call comes back, so that the turn's step is queued first and the user's
// message right after it: whichever the loop takes first, the report is
// never recorded after the user's message.
func TestSR13PlainTextReportCheckAndRecordAreTwoLoopSteps(t *testing.T) {
	const report = "Final report: #42 is merged and this session is ending."
	var phase atomic.Int32
	var armed atomic.Bool
	var rtp atomic.Pointer[Runtime]
	ui := &sr13UI{fakeUI: &fakeUI{input: make(chan string, 16)}}
	ui.onStream = func(kind, delta string) {
		// The turn's goroutine, right after the closing call came back and
		// before its stillClosing check is queued (loop.go: r.ui.Stream("",
		// "") after r.complete).
		if kind != "" || !armed.CompareAndSwap(true, false) {
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
			r.post(func() { r.userMessage(event.UserMessageData{Text: "is #42 merged?"}) })
			close(gate)
		}()
	}
	model := audit2Model(t, func(_ string, msgs []map[string]any) reply {
		tail := lastUserText(msgs)
		switch {
		case strings.Contains(tail, "The session is ending now") && phase.CompareAndSwap(1, 2):
			armed.Store(true)
			return reply{text: report}
		case phase.CompareAndSwap(2, 3):
			return say("Yes, #42 is merged and the tests are green.")
		case strings.Contains(tail, "finished a turn") && phase.CompareAndSwap(0, 1):
			return stopsWhenDone()
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
	if phase.Load() < 2 {
		t.Fatalf("setup: no closing call (phase %d)", phase.Load())
	}
	said := s.ui.said()
	var asked, reported int64
	important := false
	for _, ev := range mustReadCore(t, s.rt.sess.Path) {
		var d event.MessageData
		var u event.UserMessageData
		switch {
		case ev.Type == event.UserMessage && ev.Decode(&u) == nil && u.Text == "is #42 merged?":
			asked = ev.Seq
		case ev.Type == event.Message && ev.Decode(&d) == nil && strings.Contains(d.Text, report):
			reported, important = ev.Seq, d.Important
		}
	}
	if asked == 0 {
		t.Fatalf("setup: the user's message was not recorded; said %q", said)
	}
	if reported != 0 && asked < reported {
		t.Fatalf("exit %d: the plain-text closing report (seq %d, important %v) was recorded after the user's message (seq %d); said %q", code, reported, important, asked, said)
	}
}
