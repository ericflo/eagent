package web

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/integration"
	"github.com/ericflo/eagent/internal/store"
)

// fakeHosted stands in for a session this server runs.
func fakeHosted() *hosted {
	done := make(chan struct{})
	var once sync.Once
	return &hosted{cancel: func() { once.Do(func() { close(done) }) }, done: done}
}

func newSession(t *testing.T, project string) *store.Session {
	t.Helper()
	sess, err := store.Create(store.Root(project), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return sess
}

type restored struct {
	id    string
	reply string
}

func stubRestore(t *testing.T, replies map[string]string, unreadable map[string]bool) *[]restored {
	t.Helper()
	var mu sync.Mutex
	var got []restored
	oldRestore, oldReply := restoreSession, pendingReply
	restoreSession = func(s *Server, id string, reply integration.PhoneMessage) error {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, restored{id, reply.Text})
		s.mu.Lock()
		s.running[id] = fakeHosted()
		s.mu.Unlock()
		return nil
	}
	pendingReply = func(_ context.Context, _ string, info store.Info) (integration.PhoneMessage, bool, error) {
		if unreadable[info.ID] {
			return integration.PhoneMessage{}, false, errors.New("finalechat unreachable")
		}
		if text, ok := replies[info.ID]; ok {
			return integration.PhoneMessage{ID: "m-" + info.ID, Text: text}, true, nil
		}
		return integration.PhoneMessage{}, false, nil
	}
	t.Cleanup(func() { restoreSession, pendingReply = oldRestore, oldReply })
	return &got
}

// A stopping server lists what it hosts; the next one brings those back,
// with a reply sent in between, and leaves alone what it cannot bring back
// faithfully.
func TestRestartBringsBackWhatWasRunning(t *testing.T) {
	s, project := newTestServer(t)
	var ids []string
	for range 4 {
		sess := newSession(t, project)
		_ = sess.Close()
		ids = append(ids, sess.ID)
	}
	// Running elsewhere (its lock held): not this server's to bring back.
	elsewhere := newSession(t, project)
	defer elsewhere.Close()
	for _, id := range append(append([]string{}, ids...), elsewhere.ID, "1700000000000") {
		s.running[id] = fakeHosted()
	}
	s.rememberHosted()
	if _, err := os.Stat(hostedFile(project)); err != nil {
		t.Fatal("nothing written at stop")
	}

	got := stubRestore(t, map[string]string{ids[1]: "how is it going?"}, map[string]bool{ids[3]: true})
	next := New(project, nil)
	next.restoreHosted(context.Background())
	want := []restored{{ids[0], ""}, {ids[1], "how is it going?"}, {ids[2], ""}}
	if len(*got) != len(want) {
		t.Fatalf("brought back %+v, want %+v", *got, want)
	}
	for i := range want {
		if (*got)[i] != want[i] {
			t.Fatalf("brought back %+v, want %+v", *got, want)
		}
	}
	// The list is used once.
	if _, err := os.Stat(hostedFile(project)); !os.IsNotExist(err) {
		t.Fatalf("the list is still there: %v", err)
	}
	*got = nil
	New(project, nil).restoreHosted(context.Background())
	if len(*got) != 0 {
		t.Fatalf("restored again: %+v", *got)
	}
}

// The server does both on its own: it brings sessions back as it starts and
// writes them down as it stops.
func TestServeRestoresAtStartAndRemembersAtStop(t *testing.T) {
	s, project := newTestServer(t)
	sess := newSession(t, project)
	_ = sess.Close()
	s.running[sess.ID] = fakeHosted()
	s.rememberHosted()
	got := stubRestore(t, nil, nil)

	next := New(project, nil)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ListenAndServe(ctx, addr, next) }()
	for deadline := time.Now().Add(5 * time.Second); !next.isHosted(sess.ID); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the session was not brought back at start")
		}
	}
	if len(*got) != 1 || (*got)[0].id != sess.ID {
		t.Fatalf("restored %+v", *got)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the server did not stop")
	}
	raw, err := os.ReadFile(hostedFile(project))
	if err != nil || !strings.Contains(string(raw), sess.ID) {
		t.Fatalf("not written down at stop: %s %v", raw, err)
	}
}
