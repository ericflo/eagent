package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/store"
)

// k16Mirrored records that the session mirrored to the phone (its
// phone.thread), as a run with the phone on does: a stop then tells it.
func k16Mirrored(t *testing.T, info store.Info) {
	t.Helper()
	sess, _, err := store.Open(info.Path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	if _, err := sess.Append(event.New(event.PhoneThread, event.ActorHarness, event.PhoneThreadData{ThreadID: "t", ExternalID: "eagent:" + info.ID})); err != nil {
		t.Fatal(err)
	}
}

// A session whose record shows no phone (a run with the phone off writes
// phone_ledger on its session.start, as every run does, and no phone.*
// event) has its stop finished by a server whose own configuration has
// the phone on, the configuration the session's own (named nowhere)
// falls back to. Nothing is posted: whether a stop tells a phone at all
// is read from the record. It used to post a header and a closing note
// to a thread the session never had.
func TestK16AStopOfASessionThatNeverMirroredTellsNoPhone(t *testing.T) {
	s, project := newTestServer(t)
	t.Setenv("EAGENT_FINALECHAT", "")
	cfg := r11Model(t)
	cfg.Finalechat.Enabled = nil // the server's own: the phone on
	if _, err := config.Write(project, cfg); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	f := &sr6Phone{keys: map[string]bool{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		f.serve(w, r)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FINALECHAT_TOKEN", "fc_test")
	t.Setenv("FINALECHAT_URL", srv.URL)
	sess := newSession(t, project)
	if _, err := sess.Append(event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{Session: sess.ID, Cwd: project, PhoneLedger: true})); err != nil {
		t.Fatal(err)
	}
	_ = sess.Close()
	at := time.Now().UTC()
	s.mu.Lock()
	s.writeHostedList(nil, at, k12Marks(at, sess.ID))
	s.mu.Unlock()
	s.restoreHosted(context.Background())
	if s.listed(sess.ID) {
		t.Fatal("setup: the stop was not finished")
	}
	if n := calls.Load(); n != 0 {
		t.Fatalf("the session never mirrored to the phone, and finishing its stop called the phone service %d time(s) (closing notes %d)", n, len(f.notes))
	}
}
