package web

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/store"
)

// Stopping the server does not wait out the browser's live stream (it used
// to hold shutdown for its full timeout).
func TestStopIsNotHeldByALiveStream(t *testing.T) {
	s, project := newTestServer(t)
	sess, err := store.Create(store.Root(project), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_ = sess.Close()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ListenAndServe(ctx, addr, s) }()
	var resp *http.Response
	for deadline := time.Now().Add(5 * time.Second); ; {
		if resp, err = http.Get("http://" + addr + "/api/sessions/" + sess.ID + "/stream"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	defer resp.Body.Close()
	start := time.Now()
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the server did not stop")
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("stopping took %s with a live stream open", took)
	}
}
