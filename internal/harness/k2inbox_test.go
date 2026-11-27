package harness

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

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
