package harness

// The harness suite once published real session artifacts to the production
// FinaleChat account: Run started the artifact publisher, whose sweep loads
// the project config from disk (a bare TempDir means defaults, artifacts
// ON) and resolves credentials via ~/.config/finalechat/config.json. This
// test runs a full phone-mirror session the way the leaking tests do and
// proves nothing escapes: no publisher state is left behind and no
// non-loopback HTTP request is attempted.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
)

func TestHarnessSessionPublishesNothingOutward(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	t.Setenv("EAGENT_TEST_FC", "fc_test")
	deniedBefore := testDeniedHTTP.Load()
	project := t.TempDir()
	brain := func(model string, msgs []map[string]any) reply {
		all := allText(msgs)
		if model == "orch" {
			return reply{calls: []event.ToolCall{tc("yield", `{"done":true,"reason":"nothing to do"}`)}}
		}
		if strings.Contains(all, "nothing to do") && !strings.Contains(all, "Nothing to do.") {
			return reply{calls: []event.ToolCall{tc("send_message", `{"text":"Nothing to do."}`)}}
		}
		return reply{calls: []event.ToolCall{tc("hold", `{}`)}}
	}
	s := newScripted(brain)
	defer s.srv.Close()
	fp := newFakePhone(true)
	defer fp.srv.Close()
	cfg := fakePhoneConfig(t, project, s.srv.URL, fp)
	ui := &fakeUI{input: make(chan string)}
	rt, err := New(cfg, Options{Project: project, Interactive: false, Prompt: "idle"}, ui)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if code := rt.Run(ctx); code != 0 {
		_, _, logs := ui.snapshot()
		t.Fatalf("exit %d logs=%v", code, logs)
	}
	// The mirror talked to the fake phone, and nothing else.
	if n := fp.postCount(); n == 0 {
		t.Fatal("the mirror never reached the fake phone; the test proved nothing")
	}
	// The publisher leaves a journal (publication.json) and takes the scan
	// lock (scan.lock) on every sweep; other integration workers share the
	// directory but never touch those paths. Neither may exist here.
	var publisherTraces []string
	stateRoot := filepath.Join(project, ".agents", "eagent", "finalechat-state")
	_ = filepath.WalkDir(stateRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if name := d.Name(); name == "publication.json" || name == "scan.lock" {
			rel, _ := filepath.Rel(project, path)
			publisherTraces = append(publisherTraces, rel)
		}
		return nil
	})
	if len(publisherTraces) != 0 {
		t.Fatalf("the artifact publisher ran during a harness test: %v", publisherTraces)
	}
	if n := testDeniedHTTP.Load() - deniedBefore; n != 0 {
		t.Fatalf("%d non-loopback HTTP requests were attempted during the session", n)
	}
}
