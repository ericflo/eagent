package harness

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
)

// A Stop during the server's shutdown kills a session's agents before it
// records their stop; if the process exits in between, the next run finds
// the agent's terminal gone. What it records then must not call a message
// still held in the agent's spool unconfirmed: it never left eagent, and
// is taken back and recorded not sent (settleExited).
func TestK3AResumeFindingAKilledAgentTakesBackItsSpool(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	useFakeRelays(t, newFakeSeat("ok"))
	project := t.TempDir()
	rt, err := New(testConfig("http://127.0.0.1:1"), Options{Project: project}, &fakeUI{})
	if err != nil {
		t.Fatal(err)
	}
	rt.append(event.New(event.RelayStart, event.ActorHarness, event.RelayStartData{Name: "claude", Profile: "claude", Agent: "claude", Dir: project, Host: "tmux", Target: "eagent-k3-claude"}))
	rt.append(event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: "claude", ID: "h1", From: "user", Mode: "after_turn", Text: "then run the linter"}))
	path := rt.sess.Path
	rt.sess.Close()
	spool := filepath.Join(path, "relays", "claude", "after_turn")
	if err := os.MkdirAll(spool, 0o755); err != nil {
		t.Fatal(err)
	}
	held := filepath.Join(spool, "1700000000000000000-h1.txt")
	if err := os.WriteFile(held, []byte("then run the linter"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := attachRelay
	attachRelay = func(relay.Spec, string, string, string, string, time.Time) (relaySeat, error) {
		return nil, errors.New("can't find session") // the Stop killed it
	}
	t.Cleanup(func() { attachRelay = old })
	rt2, err := Resume(testConfig("http://127.0.0.1:1"), Options{Project: project}, &fakeUI{}, path)
	if err != nil {
		t.Fatal(err)
	}
	defer rt2.sess.Close()
	var outcomes []event.RelayDeliveredData
	for _, ev := range rt2.st.Events {
		var d event.RelayDeliveredData
		if ev.Type == event.RelayDelivered && ev.Decode(&d) == nil && d.ID == "h1" {
			outcomes = append(outcomes, d)
		}
	}
	if _, still := os.Stat(held); len(outcomes) != 1 || outcomes[0].Outcome != event.DeliveryNotSent || still == nil {
		t.Fatalf("outcomes of the message still in the spool: %+v; still in the spool: %v", outcomes, still == nil)
	}
}
