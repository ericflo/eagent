package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
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

// A session ran on the bundle "work", which mirrors to the phone service
// at A; its record says so (phone.thread, base_url A). The bundle is then
// deleted, and the server's own configuration mirrors to another service,
// B. The user stops the session while the server shuts down (or the next
// server finishes the marked stop): runConfig falls through to the
// server's own, and since the record shows a phone.* event the stop tells
// the phone, on B, where the session never had a thread: a header and
// "eagent stopped this session" land in a thread created there, and the
// thread on A, which the user reads, is told nothing. The record names
// the service it mirrored to; the stop does not compare.
func TestSR8AStopTellsThePhoneTheSessionMirroredTo(t *testing.T) {
	s, project := newTestServer(t)
	t.Setenv("EAGENT_FINALECHAT", "")
	const a, b = "https://phone-a.invalid", "https://phone-b.invalid"
	if err := os.MkdirAll(filepath.Dir(config.File(project)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.File(project), []byte(`{"finalechat": {"base_url": "`+b+`"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sess := newSession(t, project)
	if _, err := sess.Append(event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{Session: sess.ID, Config: "work"})); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.Append(event.New(event.PhoneThread, event.ActorHarness, event.PhoneThreadData{ThreadID: "t", ExternalID: "eagent:" + sess.ID, BaseURL: a})); err != nil {
		t.Fatal(err)
	}
	_ = sess.Close()
	info, err := store.Resolve(store.Root(project), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	old := tellPhoneStopped
	t.Cleanup(func() { tellPhoneStopped = old })
	var told []string
	tellPhoneStopped = func(cfg config.Config, _ string, _ func(event.Event)) error {
		told = append(told, cfg.Finalechat.BaseURL)
		return nil
	}
	if _, _, err := stopAgents(info.Path, s.stopConfig(info), nil); err != nil {
		t.Fatal(err)
	}
	for _, at := range told {
		if at != a {
			t.Fatalf("the stop told the phone at %q; the session's thread is at %q (its record's phone.thread)", at, a)
		}
	}
}

// k16Thread is a session whose record shows its thread on the phone
// service at base; the project's file has its account at b, and the
// server, run with --config work, at a ("work", spelled another way).
func k16Thread(t *testing.T, project, a, b, base string) store.Info {
	t.Helper()
	t.Setenv("EAGENT_FINALECHAT", "")
	t.Setenv("FINALECHAT_TOKEN", "fc_test")
	t.Setenv("FINALECHAT_URL", "")
	if err := os.MkdirAll(config.BundlesDir(project), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.File(project), []byte(`{"finalechat": {"base_url": "`+b+`"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.BundlePath(project, "work"), []byte(`{"finalechat": {"base_url": "`+strings.ToUpper(a)+`:443/"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sess := newSession(t, project)
	if _, err := sess.Append(event.New(event.PhoneThread, event.ActorHarness, event.PhoneThreadData{ThreadID: "t", ExternalID: "eagent:" + sess.ID, BaseURL: base})); err != nil {
		t.Fatal(err)
	}
	_ = sess.Close()
	info, err := store.Resolve(store.Root(project), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

// The stop tells the phone on the account at the address of the session's
// thread, among the project's file (first) and the server's own
// configuration, addresses compared as the reply watcher compares them;
// with neither there, it tells nothing and says why.
func TestK16AStopTellsThePhoneOnlyOnTheAccountOfTheSessionsThread(t *testing.T) {
	const a, b, c = "https://phone-a.invalid", "https://phone-b.invalid", "https://phone-c.invalid"
	for _, tc := range []struct{ base, want string }{{a, "work"}, {b, "file"}, {c, ""}} {
		_, project := newTestServer(t)
		var logged []string
		s := New(project, func(f string, args ...any) { logged = append(logged, fmt.Sprintf(f, args...)) })
		s.Bundle = "work"
		info := k16Thread(t, project, a, b, tc.base)
		cfg := s.stopConfig(info)
		got := ""
		switch {
		case cfg == nil:
		case cfg.Finalechat.BaseURL == b:
			got = "file"
		default:
			got = "work"
		}
		if got != tc.want {
			t.Fatalf("thread at %s: the stop tells on %q, want %q", tc.base, got, tc.want)
		}
		if tc.want == "" && !slices.ContainsFunc(logged, func(l string) bool { return strings.Contains(l, "not told") }) {
			t.Fatalf("the log does not say why the phone is not told: %q", logged)
		}
	}
}
