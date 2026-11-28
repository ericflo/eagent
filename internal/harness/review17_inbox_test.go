package harness

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// A run ends with a /quit still in its inbox: one written behind the stop
// that ended it (a run that is ending leaves a command for the next run),
// or one written after its last look. The quit was for that run. The next
// run, brought back with a message of the user's, does not quit on it
// before it has looked at anything: it is ignored, as a stop left by an
// earlier run is, and the message gets its answer. (The run ended at
// once, with no model call made for the message the server had reported
// as delivered.)
func TestAQuitLeftInTheInboxDoesNotEndTheNextRun(t *testing.T) {
	for _, quit := range []string{"/quit", "/Exit now", "/q"} {
		for _, how := range []string{"behind the stop that ended the run", "after the run's last look"} {
			t.Run(quit+" "+how, func(t *testing.T) {
				t.Setenv("EAGENT_TEST_KEY", "x")
				useFakeRelays(t, newFakeSeat("ok"))
				model := newScripted(func(_ string, m []map[string]any) reply {
					if m[len(m)-1]["role"] == "tool" {
						return hold()
					}
					return say("Looking at the login bug now.")
				})
				t.Cleanup(model.srv.Close)
				project := t.TempDir()
				rt, err := New(testConfig(model.srv.URL), Options{Project: project, Interactive: true}, &fakeUI{input: make(chan string)})
				if err != nil {
					t.Fatal(err)
				}
				path := rt.sess.Path
				ran := make(chan struct{})
				go func() { rt.Run(context.Background()); close(ran) }()
				post := func(msg InboxMessage) {
					t.Helper()
					if err := PostInbox(path, msg); err != nil {
						t.Fatal(err)
					}
				}
				rt.sync(func() {
					post(InboxMessage{Type: "stop", From: "web"})
					if how == "behind the stop that ended the run" {
						post(InboxMessage{Type: "message", Text: quit, From: "web"})
					}
					rt.pollInbox()
				})
				select {
				case <-ran:
				case <-time.After(10 * time.Second):
					t.Fatal("setup: the first run did not end on its stop")
				}
				if how == "after the run's last look" {
					post(InboxMessage{Type: "message", Text: quit, From: "web"})
				}
				ui := &fakeUI{}
				rt2, err := Resume(testConfig(model.srv.URL), Options{Project: project, Prompt: "please fix the login bug"}, ui, path)
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				done := make(chan int, 1)
				go func() { done <- rt2.Run(ctx) }()
				waitFor(t, "the answer to the user's message, or the run's end", func() bool {
					return strings.Contains(strings.Join(ui.said(), "\n"), "Looking at the login bug") || len(done) > 0
				})
				if told := strings.Join(ui.said(), "\n"); !strings.Contains(told, "Looking at the login bug") {
					t.Fatalf("the run brought back with the user's message ended with %d model call(s) and told the user: %q", model.count(testModel), told)
				}
				if left, _ := os.ReadDir(InboxDir(path)); len(left) != 0 {
					t.Errorf("%d file(s) left in the inbox", len(left))
				}
			})
		}
	}
}
