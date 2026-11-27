package harness

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
)

// r13Service is the fake Finalechat as the service answers once the user
// has deleted the thread from the app and a post recreated it: the thread
// has a new id, and a read after a message the thread no longer has says
// the anchor is unknown and pages from the thread's first message. While
// hold is set, a read of the thread waits on it and then comes back empty.
type r13Service struct {
	fp   *phoneFake
	srv  *httptest.Server
	mu   sync.Mutex
	id   string
	hold chan struct{}
	held int
}

func newR13Service(t *testing.T, fp *phoneFake) *r13Service {
	s := &r13Service{fp: fp, id: "t1"}
	s.srv = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *r13Service) handle(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/messages") && r.Method == http.MethodGet {
		s.mu.Lock()
		hold := s.hold
		if hold != nil {
			s.held++
		}
		s.mu.Unlock()
		if hold != nil {
			select {
			case <-hold:
			case <-r.Context().Done():
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"messages":[],"has_more":false,"timed_out":true}`))
			return
		}
		after := r.URL.Query().Get("after")
		s.fp.mu.Lock()
		known := after == ""
		var page []map[string]any
		for _, m := range s.fp.messages {
			known = known || m["id"] == after
			if m["sender"] == "user" {
				page = append(page, phoneCopy(m))
			}
		}
		s.fp.mu.Unlock()
		if !known {
			if page == nil {
				page = []map[string]any{}
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"messages": page, "has_more": false, "timed_out": false, "anchor_unknown": true})
			return
		}
	}
	rec := httptest.NewRecorder()
	s.fp.handle(rec, r)
	body := rec.Body.Bytes()
	s.mu.Lock()
	id := s.id
	s.mu.Unlock()
	if id != "t1" {
		body = bytes.ReplaceAll(body, []byte(`"t1"`), []byte(`"`+id+`"`))
	}
	for k, v := range rec.Header() {
		w.Header()[k] = v
	}
	w.WriteHeader(rec.Code)
	_, _ = w.Write(body)
}

// deleteThread is the user deleting the thread from the app: its messages
// and keys go, and the next post creates it again under a new id.
func (s *r13Service) deleteThread(newID string) {
	s.fp.mu.Lock()
	s.fp.messages = nil
	s.fp.keys = map[string]string{}
	s.fp.qkeys = map[string]string{}
	s.fp.mu.Unlock()
	s.mu.Lock()
	s.id = newID
	s.mu.Unlock()
}

// r13TookPhone reports whether the session took in the phone message id.
func r13TookPhone(g *phoneRig, id string) bool {
	took := false
	g.rt.sync(func() {
		for _, ev := range g.rt.st.Events {
			var d event.UserMessageData
			if ev.Type == event.UserMessage && ev.Decode(&d) == nil && d.PhoneID == id {
				took = true
			}
		}
	})
	return took
}

// Finding 18: the user deletes the thread from the app; a post that does
// not look at the thread it landed in (the mirror of a terminal message)
// recreates it, and the user writes there. A message of eagent's posted
// next sees the new thread; the reply poll still reads that thread from
// its first message, so what the user wrote before eagent's message is
// taken, not passed over.
func TestR13APhoneMessageInARecreatedThreadIsNotPassedOver(t *testing.T) {
	setFor(t, &phonePollFloor, 5*time.Millisecond)
	fp := newPhoneFake(false)
	t.Cleanup(fp.srv.Close)
	svc := newR13Service(t, fp)
	svc.mu.Lock()
	svc.hold = make(chan struct{})
	svc.mu.Unlock()
	gate := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	t.Cleanup(release)
	var answered atomic.Bool
	brain := func(_ string, msgs []map[string]any) reply {
		if !phoneLastIsTool(msgs) && strings.Contains(lastUserText(msgs), "kept for you") && answered.CompareAndSwap(false, true) {
			return reply{block: gate, calls: say("It is green.").calls}
		}
		return hold()
	}
	g := phoneStart(t, fp, "", true, brain, func(c *config.Config) { c.Finalechat.BaseURL = svc.srv.URL })
	waitFor(t, "the reply poll's first read", func() bool {
		svc.mu.Lock()
		defer svc.mu.Unlock()
		return svc.held == 1
	})
	svc.deleteThread("t2")
	g.ui.input <- "@e is the build green?"
	waitFor(t, "the terminal message's mirror to recreate the thread", func() bool {
		fp.mu.Lock()
		defer fp.mu.Unlock()
		return len(fp.messages) == 1
	})
	lost := r10Says(fp, r10Msg("the lost words"))[0]
	release()
	waitFor(t, "eagent's message in the recreated thread", func() bool {
		var p *phone
		g.rt.sync(func() { p = g.rt.phone })
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.threadID == "t2"
	})
	svc.mu.Lock()
	held := svc.hold
	svc.hold = nil
	svc.mu.Unlock()
	close(held)
	later := r10Says(fp, r10Msg("later words"))[0]
	waitFor(t, "the later message to be taken", func() bool { return r13TookPhone(g, later) })
	if !r13TookPhone(g, lost) {
		t.Fatalf("the message written in the recreated thread before eagent's was passed over; logs %v", g.ui.logged())
	}
}
