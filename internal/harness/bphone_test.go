package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/store"
)

// bphoneOlder rewrites a stopped session's record as an eagent without the
// ledger would have left it: no phone_ledger on its first writes, no
// phone.read.
func bphoneOlder(t *testing.T, path string) {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(path, "*.jsonl"))
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var out []byte
		for _, line := range bytes.Split(raw, []byte("\n")) {
			if len(bytes.TrimSpace(line)) == 0 {
				continue
			}
			var ev map[string]any
			if err := json.Unmarshal(line, &ev); err != nil {
				t.Fatal(err)
			}
			if ev["type"] == event.PhoneRead {
				continue
			}
			if d, ok := ev["data"].(map[string]any); ok {
				delete(d, "phone_ledger")
			}
			b, _ := json.Marshal(ev)
			out = append(append(out, b...), '\n')
		}
		if err := os.WriteFile(file, out, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// bphoneRun resumes the session from the terminal; stop ends the run.
func bphoneRun(t *testing.T, c func() *Runtime) (rt *Runtime, stop func()) {
	t.Helper()
	rt = c()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	done := make(chan struct{})
	go func() { rt.Run(ctx); close(done) }()
	stop = func() {
		cancel()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			t.Fatal("the session did not stop")
		}
	}
	return rt, stop
}

// bphoneAnchor is the message the newest run's mirror read on from, or
// false while it has not said.
func bphoneAnchor(path string) (string, bool) {
	evs, _ := store.Read(path)
	id, ok := "", false
	for _, ev := range evs {
		switch ev.Type {
		case event.SessionResume:
			id, ok = "", false
		case event.PhoneRead:
			var d event.PhoneReadData
			if !ok && ev.Decode(&d) == nil {
				id, ok = d.MessageID, true
			}
		}
	}
	return id, ok
}

// A session last run by an eagent without the ledger, resumed from the
// terminal while two messages from the app wait: both are taken, in
// order, once; a second resume takes neither again; and with nothing
// waiting the mirror reads on from its own post, as before.
func TestAnOlderRecordResumedAtTheTerminalTakesTheMessagesThatWaited(t *testing.T) {
	t.Run("two waiting", func(t *testing.T) {
		cfg, project, path, fp := stoppedRelaySession(t)
		bphoneOlder(t, path)
		first := fp.userSays("the checklist is A, B, C", nil)
		second := fp.userSays("are you still there?", nil)
		resume := func() *Runtime {
			useFakeRelays(t, newFakeSeat("Done."))
			rt, err := Resume(cfg, Options{Project: project, Interactive: true}, &fakeUI{input: make(chan string, 4)}, path)
			if err != nil {
				t.Fatal(err)
			}
			return rt
		}
		rt, stop := bphoneRun(t, resume)
		waitFor(t, "both in the record", func() bool { return len(r6PhoneIDs(path)) >= 2 })
		rt.sync(func() {})
		stop()
		if got := r6PhoneIDs(path); len(got) != 2 || got[0] != first || got[1] != second {
			t.Fatalf("recorded %v, want %v", got, []string{first, second})
		}
		gets := fp.getCount()
		_, stop = bphoneRun(t, resume)
		waitFor(t, "the second run's cursor", func() bool { a, ok := bphoneAnchor(path); return ok && a == second })
		waitFor(t, "the second run's poll", func() bool { return fp.getCount() > gets })
		stop()
		if got := r6PhoneIDs(path); len(got) != 2 {
			t.Fatalf("after the second resume recorded %v", got)
		}
	})
	t.Run("nothing waiting", func(t *testing.T) {
		cfg, project, path, fp := stoppedRelaySession(t)
		bphoneOlder(t, path)
		posts := fp.postCount()
		_, stop := bphoneRun(t, func() *Runtime {
			useFakeRelays(t, newFakeSeat("Done."))
			rt, err := Resume(cfg, Options{Project: project, Interactive: true}, &fakeUI{input: make(chan string, 4)}, path)
			if err != nil {
				t.Fatal(err)
			}
			return rt
		})
		waitFor(t, "the cursor", func() bool { _, ok := bphoneAnchor(path); return ok })
		stop()
		anchor, _ := bphoneAnchor(path)
		resumed := fp.postsWhere(func(p map[string]any) bool { return p["body"] == "eagent session resumed here." })
		if len(resumed) != 1 || fp.postCount() <= posts {
			t.Fatalf("resumed posts %v", resumed)
		}
		fp.mu.Lock()
		var own string
		for _, m := range fp.messages {
			if m["body"] == "eagent session resumed here." {
				own, _ = m["id"].(string)
			}
		}
		fp.mu.Unlock()
		if anchor != own || own == "" {
			t.Fatalf("read on from %q, want its own post %q", anchor, own)
		}
		if got := r6PhoneIDs(path); len(got) != 0 {
			t.Fatalf("recorded %v", got)
		}
	})
}
