package harness

import (
	"context"
	"os"
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// k1Words returns the inbox ids of the user messages on record with these
// words. Loop goroutine (or a runtime whose loop has stopped).
func k1Words(rt *Runtime, text string) []string {
	var ids []string
	for _, ev := range rt.st.Events {
		var d event.UserMessageData
		if ev.Type == event.UserMessage && ev.Decode(&d) == nil && d.Text == text {
			ids = append(ids, d.Inbox)
		}
	}
	return ids
}

// D3: words handed to a running session with Take are on its record, with
// their inbox file's id, and the file is gone, when Take returns. Handed
// to a run that has begun to end, nothing is written and Take says so, so
// the caller can resume the session with them without their being taken
// twice.
func TestK1TakeIsTakenAtOnceOrNotAtAll(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	ui := &fakeUI{input: make(chan string)}
	rt, err := New(testConfig("http://127.0.0.1:1"), Options{Project: t.TempDir(), Interactive: true}, ui)
	if err != nil {
		t.Fatal(err)
	}
	ran := make(chan struct{})
	go func() { rt.Run(context.Background()); close(ran) }()
	took, err := rt.Take(InboxMessage{Type: "message", Text: "k1 one more thing", From: "web"})
	if !took || err != nil {
		t.Fatalf("a running session's Take: %v, %v", took, err)
	}
	var ids []string
	rt.sync(func() { ids = k1Words(rt, "k1 one more thing") })
	if len(ids) != 1 || ids[0] == "" {
		t.Fatalf("on record with inbox ids %q, want once, with its file's", ids)
	}
	if left, _ := os.ReadDir(InboxDir(rt.sess.Path)); len(left) != 0 {
		t.Fatalf("%d file(s) left in the inbox", len(left))
	}
	rt.post(func() { rt.beginShutdown("quit", 0) })
	took, err = rt.Take(InboxMessage{Type: "message", Text: "k1 and another", From: "web"})
	if took || err != nil {
		t.Fatalf("an ending session's Take: %v, %v", took, err)
	}
	<-ran
	if left, _ := os.ReadDir(InboxDir(rt.sess.Path)); len(left) != 0 {
		t.Fatalf("%d file(s) written to the inbox of a run that was ending", len(left))
	}
	if ids := k1Words(rt, "k1 and another"); len(ids) != 0 {
		t.Fatalf("the ending run took the words %d times", len(ids))
	}
}
