package harness

import (
	"os"
	"strings"
	"sync"
	"testing"
)

// Finding 8 of the ninth review (main has it too): submissions written at
// once each get a file of their own, under a name and an id no other has.
func TestR9PostsAtOnceEachKeepTheirOwnFile(t *testing.T) {
	path := t.TempDir()
	const writers, each = 8, 50
	var wg sync.WaitGroup
	errs := make(chan error, writers*each)
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				if err := PostInbox(path, InboxMessage{Type: "message", Text: "r9 post"}); err != nil {
					errs <- err
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("a post failed: %v", err)
	}
	entries, err := os.ReadDir(InboxDir(path))
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, e := range entries {
		raw, err := os.ReadFile(InboxDir(path) + "/" + e.Name())
		if err != nil || !strings.HasSuffix(e.Name(), ".json") {
			t.Fatalf("%s: %v", e.Name(), err)
		}
		id := inboxFileID(raw, e.Name())
		if id == "" || ids[id] {
			t.Fatalf("%s: id %q is missing or taken twice", e.Name(), id)
		}
		ids[id] = true
	}
	if len(ids) != writers*each {
		t.Fatalf("%d files for %d posts", len(ids), writers*each)
	}
}
