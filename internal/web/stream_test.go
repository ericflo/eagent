package web

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/store"
)

// A followed session's status counts the subsessions it has now: one a
// rollover opens after the stream started is in the next status.
func TestStreamStatusFollowsRollovers(t *testing.T) {
	// Set before the server starts: each stream reads it once.
	old := streamStatusEvery
	streamStatusEvery = 50 * time.Millisecond
	t.Cleanup(func() { streamStatusEvery = old })
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
	defer cancel()
	go func() { _ = ListenAndServe(ctx, addr, s) }()
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
	lines := bufio.NewScanner(resp.Body)
	lines.Buffer(make([]byte, 1<<20), 1<<20)
	status := func() int {
		kind := ""
		for lines.Scan() {
			line := lines.Text()
			if k, ok := strings.CutPrefix(line, "event: "); ok {
				kind = k
			} else if d, ok := strings.CutPrefix(line, "data: "); ok && kind == "status" {
				var v struct {
					Subsessions int `json:"subsessions"`
				}
				if err := json.Unmarshal([]byte(d), &v); err != nil {
					t.Fatal(err)
				}
				return v.Subsessions
			}
		}
		t.Fatal("the stream ended")
		return 0
	}
	first := status()
	if err := os.WriteFile(filepath.Join(sess.Path, "zz-next.jsonl"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	// A status sent just before the file appeared may still be on its way.
	deadline := time.Now().Add(5 * time.Second)
	for got := status(); got != first+1; got = status() {
		if time.Now().After(deadline) {
			t.Fatalf("subsessions %d, then %d", first, got)
		}
	}
}
