package web

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/harness"
	"github.com/ericflo/eagent/internal/store"
)

// Finding 30: two messages sent from the browser at once to a session
// nobody is running. The first resumes it; the second waits for that and
// goes to the inbox of the run the first started. It used to try to resume
// the session too, fail on its lock, and be lost.
func TestTwoMessagesAtOnceToAStoppedSessionAreBothDelivered(t *testing.T) {
	s, project := newTestServer(t)
	sess, err := store.Create(store.Root(project), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	id := sess.ID
	sess.Close()
	info, err := s.resolve(id)
	if err != nil {
		t.Fatal(err)
	}
	entered, gate, again := make(chan struct{}), make(chan struct{}), make(chan struct{}, 1)
	resumes := 0
	resume := func() error {
		resumes++
		if resumes > 1 {
			again <- struct{}{}
			return nil
		}
		close(entered)
		<-gate
		s.mu.Lock()
		s.running[id] = &hosted{} // as s.run does once the resume succeeded
		s.mu.Unlock()
		return nil
	}
	type result struct {
		how string
		err error
	}
	first, second := make(chan result, 1), make(chan result, 1)
	go func() {
		how, err := s.deliver(info, harness.InboxMessage{Type: "message", Text: "one", From: "web"}, resume)
		first <- result{how, err}
	}()
	<-entered
	go func() {
		how, err := s.deliver(info, harness.InboxMessage{Type: "message", Text: "two", From: "web"}, resume)
		second <- result{how, err}
	}()
	select {
	case <-again:
		t.Fatal("the second message tried to resume the session while the first was resuming it")
	case r := <-second:
		t.Fatalf("the second message did not wait for the first's resume: %+v", r)
	case <-time.After(20 * time.Millisecond):
	}
	close(gate)
	if r := <-first; r.err != nil || r.how != "resumed" {
		t.Fatalf("first: %+v", r)
	}
	if r := <-second; r.err != nil || r.how != "inbox" {
		t.Fatalf("second: %+v, want it in the running session's inbox", r)
	}
	if resumes != 1 {
		t.Fatalf("resumed %d times", resumes)
	}
}

// The page clears the box before it sends; when the request fails the
// words go back into the box instead of being lost.
func TestThePageKeepsTheWordsWhenASendFails(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	src, err := os.ReadFile(filepath.Join("static", "app.js"))
	if err != nil {
		t.Fatal(err)
	}
	send := regexp.MustCompile(`(?s)async function send\(\) \{.*?\n\}\n`).FindString(string(src))
	if send == "" {
		t.Fatal("app.js has no send()")
	}
	script := `
const ta = {value: '  ship it  ', style: {}, dataset: {sid: 's1'}};
const $ = () => ta;
const S = {sid: 's1', detail: null, drafts: {}};
const clientCaps = () => ({});
let told = '';
const toast = (m) => { told = m; };
const api = async () => { throw new Error('session is already being run'); };
` + send + `
send().then(() => {
  if (ta.value !== 'ship it') { console.error('the box holds ' + JSON.stringify(ta.value)); process.exit(1); }
  if (!told) { console.error('nothing was shown'); process.exit(1); }
});
`
	cmd := exec.Command("node", "-e", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, strings.TrimSpace(string(out)))
	}
}
