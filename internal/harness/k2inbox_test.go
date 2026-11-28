package harness

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// Words handed to a running session with Take whose file the poll cannot
// read are not reported taken: the file is taken back and Take says so,
// so the browser keeps them. It used to report them taken, and a run that
// ended next left them in its inbox.
func TestK2TakeWhosePollCannotReadItsFileSaysSo(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("runs as root: an unreadable directory does not stop root")
	}
	t.Setenv("EAGENT_TEST_KEY", "x")
	ui := &fakeUI{input: make(chan string)}
	rt, err := New(testConfig("http://127.0.0.1:1"), Options{Project: t.TempDir(), Interactive: true}, ui)
	if err != nil {
		t.Fatal(err)
	}
	ran := make(chan struct{})
	go func() { rt.Run(context.Background()); close(ran) }()
	defer func() { ui.input <- "/quit"; <-ran }()
	dir := InboxDir(rt.sess.Path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Files can be written into it and removed, not listed or read.
	if err := os.Chmod(dir, 0o300); err != nil {
		t.Fatal(err)
	}
	took, err := rt.Take(InboxMessage{Type: "message", Text: "k2 unread", From: "web"})
	os.Chmod(dir, 0o755)
	if took || err == nil {
		t.Fatalf("Take: %v, %v; want it to say the words were not taken", took, err)
	}
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Fatalf("%d file(s) left in the inbox", len(left))
	}
}

// Finding 14 of the fifteenth review: the web page's composer, for a
// running session, names commands; each must be one eagent carries out
// when it comes from the web, with the agent running. /help from the web
// is the agent's own (or words for eagent's model), so it lists nothing of
// eagent's there.
func TestK2TheComposerNamesOnlyCommandsEagentTakesFromTheWeb(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "web", "static", "app.js"))
	if err != nil {
		t.Fatal(err)
	}
	hint := regexp.MustCompile(`'Goes to the agent word for word[^']*'`).FindString(string(src))
	if hint == "" {
		t.Fatal("app.js has no composer hint for a running session")
	}
	cmds := regexp.MustCompile(`/[a-z?-]+`).FindAllString(hint, -1)
	if len(cmds) == 0 {
		t.Fatalf("the hint names no command: %s", hint)
	}
	useFakeRelays(t, newFakeSeat("one"))
	rt := newLaunchRuntime(t)
	for _, cmd := range cmds {
		own := false
		rt.sync(func() { own = rt.ownCommand(cmd, "web") })
		if !own {
			t.Errorf("the hint %s names %s, which from the web is not eagent's", hint, cmd)
		}
	}
}
