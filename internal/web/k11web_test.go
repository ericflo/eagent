package web

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/integration"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/store"
)

// The user pressed Stop while `eagent serve` was stopping, and the server
// exited before the run let go (its 10 s wait ran out, or a second Ctrl+C
// quit it at once): the list it leaves marks the stop. Before the next
// serve starts, the user resumes the session at the terminal (`eagent -c`),
// asks the agent for more work, and leaves with Ctrl+C, which ends the run
// and leaves the agent working in its terminal for the next resume. The
// next serve reads the old list and "finishes" the stop the user asked for
// an earlier run: it kills the agent the later run left working, records
// relay.end "the user stopped the session" for a run the user never
// stopped, and settles the send the agent is working on as unconfirmed.
// The list says when it was written (At), and the record shows a run after
// it; the mark no longer stands for what the user wants.
func TestSR5WebAStaleStopMarkKillsTheAgentOfALaterRun(t *testing.T) {
	s, project := newTestServer(t)
	kills := make(chan string, 8)
	old := killAgent
	killAgent = func(host, target string) error { kills <- host + " " + target; return nil }
	t.Cleanup(func() { killAgent = old })
	info := srAgentSession(t, project)
	// The list the exiting server left, the stop marked.
	s.mu.Lock()
	s.writeHostedList([]string{info.ID}, time.Now().Add(-time.Hour).UTC(), k12Marks(time.Now().Add(-time.Hour).UTC(), info.ID))
	s.mu.Unlock()
	// The later run, at the terminal: resumed, the agent picked up, a
	// message typed to it, and Ctrl+C (the agent keeps working).
	sess, _, err := store.Open(info.Path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range []event.Event{
		event.New(event.SessionResume, event.ActorHarness, event.SessionResumeData{Interactive: true}),
		event.New(event.RelayStart, event.ActorHarness, event.RelayStartData{Name: "claude", Profile: "claude", Agent: "claude", Dir: project, Host: "tmux", Target: "eagent-sr-claude", Resumed: true}),
		event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "now fix the failing tests"}),
		event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: "claude", ID: "s1", From: "user", Text: "now fix the failing tests"}),
		event.New(event.SessionEnd, event.ActorHarness, event.SessionEndData{Reason: "interrupted"}),
	} {
		if _, err := sess.Append(ev); err != nil {
			t.Fatal(err)
		}
	}
	_ = sess.Close()
	oldRestore := restoreSession
	restoreSession = func(*Server, string, integration.PhoneMessage) error { return nil }
	t.Cleanup(func() { restoreSession = oldRestore })
	next := New(project, nil)
	next.restoreHosted(context.Background())
	var killed []string
	for len(kills) > 0 {
		killed = append(killed, <-kills)
	}
	evs, err := store.Read(info.Path)
	if err != nil {
		t.Fatal(err)
	}
	var ends []string
	for _, ev := range evs {
		var d event.RelayEndData
		if ev.Type == event.RelayEnd && ev.Decode(&d) == nil {
			ends = append(ends, d.Reason)
		}
	}
	st := state.Replay(evs)
	if len(killed) > 0 || len(ends) > 0 || len(st.ActiveRelays()) == 0 {
		t.Fatalf("a stop marked for the run before the terminal's resume killed the agent the later run left working (kills %q), recorded relay.end %q, and settled the send it was working on (outcome %q)", killed, ends, st.SendOutcome("s1"))
	}
}

// A marked stop the next server cannot finish (here, the record cannot be
// written) stays marked on the list, for the start after; one it finishes
// comes off. The list used to be removed before the stop was tried, so a
// failure, or a cut partway, lost the mark.
func TestK11AMarkedStopThatFailsStaysMarked(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("runs as root: a read-only file does not stop root from writing it")
	}
	s, project := newTestServer(t)
	k5Kills(t)
	info := srAgentSession(t, project)
	s.mu.Lock()
	s.writeHostedList([]string{info.ID}, time.Now().UTC(), k12Marks(time.Now().UTC(), info.ID))
	s.mu.Unlock()
	files, _ := filepath.Glob(filepath.Join(info.Path, "*.jsonl"))
	for _, f := range files {
		if err := os.Chmod(f, 0o444); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(f, 0o644) })
	}
	next := New(project, nil)
	next.restoreHosted(context.Background())
	next.mu.Lock()
	list, ok := next.readHosted()
	next.mu.Unlock()
	if _, marked := list.mark(info.ID); !ok || !marked {
		t.Fatalf("the stop could not be finished and its mark is gone: %+v (%v)", list, ok)
	}
	for _, f := range files {
		_ = os.Chmod(f, 0o644)
	}
	again := New(project, nil)
	again.restoreHosted(context.Background())
	if got := srActiveAgents(t, info); len(got) != 0 {
		t.Fatalf("the start after did not finish the stop: %q still running", got)
	}
	if got := k3Listed(t, project); len(got) != 0 {
		t.Fatalf("the finished stop is still listed: %q", got)
	}
}

// The next server finishing a marked stop tells the session's phone, as
// the Stop itself does.
func TestK11TheNextServerFinishingAStopTellsThePhone(t *testing.T) {
	s, project := newTestServer(t)
	k5Kills(t)
	var told []string
	old := tellPhoneStopped
	tellPhoneStopped = func(_ config.Config, path string, _ func(event.Event)) { told = append(told, filepath.Base(path)) }
	t.Cleanup(func() { tellPhoneStopped = old })
	info := srAgentSession(t, project)
	s.mu.Lock()
	s.writeHostedList([]string{info.ID}, time.Now().UTC(), k12Marks(time.Now().UTC(), info.ID))
	s.mu.Unlock()
	New(project, nil).restoreHosted(context.Background())
	if !slices.Equal(told, []string{info.ID}) {
		t.Fatalf("the phone was told of %q, want the session once", told)
	}
}

// A send still waiting on an agent the record shows exited is settled by a
// Stop during the shutdown as one whose agent exited, not as one whose
// agent the Stop stopped.
func TestK11ASendOfAnExitedAgentIsSettledAsExited(t *testing.T) {
	s, project := newTestServer(t)
	k5Kills(t)
	info := srAgentSession(t, project)
	sess, _, err := store.Open(info.Path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range []event.Event{
		event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: "claude", ID: "s1", From: "user", Text: "run the tests"}),
		event.New(event.RelayState, event.ActorHarness, event.RelayStateData{Name: "claude", State: "exited", Detail: "the process ended"}),
	} {
		if _, err := sess.Append(ev); err != nil {
			t.Fatal(err)
		}
	}
	_ = sess.Close()
	k7Shutdown(t, s, info)
	if w := do(s, "POST", "/api/sessions/"+info.ID+"/stop", nil, nil); w.Code != http.StatusOK {
		t.Fatalf("stop: %d %s", w.Code, w.Body)
	}
	evs, err := store.Read(info.Path)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range evs {
		var d event.RelayDeliveredData
		if ev.Type == event.RelayDelivered && ev.Decode(&d) == nil && d.ID == "s1" && !strings.Contains(d.Detail, "exited") {
			t.Fatalf("the send to an agent that had exited is settled as %q", d.Detail)
		}
	}
}

// A message the user sent from the phone is held for the end of Claude's
// turn (in its spool; the phone's receipt reads "held"). The server's
// shutdown ends the run, which leaves the message held: the agent's hook
// still hands it over at the turn's end, so "held" stays true. The user
// then presses Stop: stopAgents takes the message back unread and records
// it not_sent, and kills the agent. Nothing tells the phone, so its receipt
// still says the message is held for the agent, which will never read it;
// the app's "Not delivered" and Retry never show. The run's own end does
// tell the phone for each send it records not sent ("its receipt turns to
// failed, before the closing note", harness runtime.go finish).
func TestSR5WebAPhoneMessageTheShutdownStopTakesBackStaysHeldOnThePhone(t *testing.T) {
	s, project := newTestServer(t)
	var mu sync.Mutex
	var bodies []string
	fc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, r.Method+" "+r.URL.Path+" "+string(raw))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(fc.Close)
	t.Setenv("EAGENT_FINALECHAT", "")
	t.Setenv("SR5_FC_TOKEN", "test-token")
	if err := os.MkdirAll(filepath.Dir(config.File(project)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.File(project), []byte(`{"finalechat": {"base_url": "`+fc.URL+`", "token_env": "SR5_FC_TOKEN"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	old := killAgent
	killAgent = func(string, string) error { return nil }
	t.Cleanup(func() { killAgent = old })
	info := srAgentSession(t, project)
	sess, _, err := store.Open(info.Path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	um, err := sess.Append(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "then run the linter", Source: "finalechat", PhoneID: "pm1"}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.Append(event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: "claude", ID: "h1", From: "user", Mode: "after_turn", Seq: um.Seq, Text: "then run the linter"})); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.Append(event.New(event.SessionEnd, event.ActorHarness, event.SessionEndData{Reason: "interrupted"})); err != nil {
		t.Fatal(err)
	}
	_ = sess.Close()
	spool := filepath.Join(info.Path, "relays", "claude", "after_turn")
	if err := os.MkdirAll(spool, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(spool, "1700000000000000000-h1.txt"), []byte("then run the linter"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The shutdown has begun; the run here has ended and the session is on
	// the list the next server brings back.
	s.mu.Lock()
	s.writeHostedList([]string{info.ID}, time.Now().UTC(), nil)
	s.mu.Unlock()
	s.resuming.Lock()
	s.stopping = true
	s.resuming.Unlock()
	if w := do(s, "POST", "/api/sessions/"+info.ID+"/stop", nil, nil); w.Code != http.StatusOK {
		t.Fatalf("stop: %d %s", w.Code, w.Body.String())
	}
	evs, err := store.Read(info.Path)
	if err != nil {
		t.Fatal(err)
	}
	if o := state.Replay(evs).SendOutcome("h1"); o != state.SendNotSent {
		t.Fatalf("setup: the held message's outcome is %q, want not sent", o)
	}
	mu.Lock()
	defer mu.Unlock()
	told := false
	for _, b := range bodies {
		told = told || strings.Contains(b, "pm1") && strings.Contains(b, "failed")
	}
	if !told {
		t.Fatalf("the record says the phone message did not go (taken back unread from the spool), but the phone was never told: its receipt still says it is held for the agent (requests to FinaleChat: %q)", bodies)
	}
	// And the closing note of a stop, after the receipts.
	note := -1
	for i, b := range bodies {
		if strings.Contains(b, "eagent stopped this session") {
			note = i
		}
	}
	if note < 0 || !strings.Contains(strings.Join(bodies[:note], "\n"), "pm1") {
		t.Fatalf("no stop's closing note after the receipts: %q", bodies)
	}
}
