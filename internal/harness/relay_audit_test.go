package harness

import (
	"strings"
	"sync"
	"testing"
)

// Phrases that hold a message for eagent are built from every agent's
// names, and an agent may have a short one ("pi"). A phrase ending in a
// name counts only where the name is a whole word, so ordinary work that
// happens to contain it is typed into the agent as before.
func TestAgentNamePhrasesMatchWholeWordsOnly(t *testing.T) {
	for _, text := range []string{
		"update pip and rerun the tests",
		"update the pipeline config",
		"restart pipeline",
		"upgrade pillow to 11",
		"not for pictures, use the svg",
		"update claude.md with the new rule",
		"update codex-rs/core to the new api",
	} {
		if relayMeta(text) || addressedToEagent(text) {
			t.Errorf("%q would be kept from the agent", text)
		}
	}
	for _, text := range []string{"update pi", "restart pi please", "upgrade pi, then restart it", "restart codex", "update claude.", "Upgrade Claude Code and carry on", "what is pi doing"} {
		if !relayMeta(text) {
			t.Errorf("%q is about the session and would be typed into the agent", text)
		}
	}
	for _, text := range []string{"this one is for you, not pi", "not for codex: how many turns so far?", "this one is for you (not claude)"} {
		if !addressedToEagent(text) {
			t.Errorf("%q is for eagent and would be typed into the agent", text)
		}
	}
}

// The message a session is started with is passed to the agent before the
// phone mirror exists. Its receipt is taken from the record once the
// thread is up, so the first message shows "delivered" like every later one.
func TestRelayFirstMessageGetsItsPhoneReceipt(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	seat := newFakeSeat("Fixed the flaky login test; main is green.")
	useFakeRelays(t, seat)
	fp := newRelayPhone()
	t.Cleanup(fp.srv.Close) // after the session has stopped: its long poll holds a request
	model := newScripted(func(string, []map[string]any) reply { return hold() })
	t.Cleanup(model.srv.Close)
	project := t.TempDir()
	rt, err := New(relayPhoneConfig(t, project, model.srv.URL, fp), Options{Project: project, Relay: "codex", Interactive: true, Prompt: "Fix the flaky login test"}, &fakeUI{input: make(chan string, 4)})
	if err != nil {
		t.Fatal(err)
	}
	relayRunLive(t, rt)
	waitFor(t, "a delivered receipt under the first message", func() bool {
		for _, m := range fp.metaPatches() {
			rr, _ := m["relay_receipts"].(map[string]any)
			for _, v := range rr {
				if v == "delivered" {
					return true
				}
			}
		}
		return false
	})
}

// Each kind of unusable reply is sent back once. When two kinds follow
// each other in one turn (text with no tool call, then a hold on a message
// kept for eagent), the third call is shown both exchanges: the second
// send-back adds to the first and does not replace it.
func TestSendBacksAddUpWithinATurn(t *testing.T) {
	var mu sync.Mutex
	var third string
	n := 0
	s := startSession(t, "", func(_ string, msgs []map[string]any) reply {
		if !strings.Contains(allText(msgs), "what is the plan") {
			return hold()
		}
		mu.Lock()
		defer mu.Unlock()
		n++
		switch n {
		case 1:
			return reply{text: "The plan is to merge both."}
		case 2:
			return hold()
		case 3:
			third = allText(msgs)
		}
		return say("The plan is to merge both.")
	}, nil)
	s.settled()
	s.type_("@e what is the plan")
	s.waitFor("the answer", func() bool { return s.ui.messageCount() == 1 })
	mu.Lock()
	defer mu.Unlock()
	for _, want := range []string{"That reply had no tool call, so nobody saw it", "Holding is not an answer to a message addressed to you"} {
		if !strings.Contains(third, want) {
			t.Fatalf("the third call was not shown %q:\n%s", want, third)
		}
	}
}
