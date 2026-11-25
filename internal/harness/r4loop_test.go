package harness

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
)

// Two messages for eagent that arrive in one wake are both owed when the
// model holds: the reminder quotes each, oldest first.
func TestAReminderNamesEveryMessageOfTheWakeOldestFirst(t *testing.T) {
	oldD, oldS := owedRetryDelays, wakeSettle
	owedRetryDelays = []time.Duration{30 * time.Millisecond, time.Hour, time.Hour}
	wakeSettle = 300 * time.Millisecond
	defer func() { owedRetryDelays, wakeSettle = oldD, oldS }()
	var mu sync.Mutex
	var reminder string
	brain := func(_ string, msgs []map[string]any) reply {
		if last := lastUserText(msgs); strings.Contains(last, "has had no answer") {
			mu.Lock()
			if reminder == "" {
				reminder = last
			}
			mu.Unlock()
		}
		return hold()
	}
	s := startSession(t, "", brain, func(cfg *config.Config) { cfg.MaxCallsPerTurn = 1 })
	defer s.stop() // before the delays are put back
	s.settled()
	s.type_("@e did the tests pass?")
	s.type_("@e and is the deploy done?")
	s.waitFor("the reminder", func() bool { mu.Lock(); defer mu.Unlock(); return reminder != "" })
	mu.Lock()
	got := reminder
	mu.Unlock()
	i, j := strings.Index(got, "did the tests pass?"), strings.Index(got, "and is the deploy done?")
	if i < 0 || j < 0 || i > j {
		t.Fatalf("the reminder does not name both messages, oldest first: %q", got)
	}
}

// A message kept for eagent that the model did not deal with stays owed
// when a second one arrives in the same turn after the record has moved
// past the first: the reminder names both.
func TestAMessageArrivingMidTurnDoesNotTakeTheOwedPlaceOfAnOlderOne(t *testing.T) {
	old := owedRetryDelays
	owedRetryDelays = []time.Duration{30 * time.Millisecond, time.Hour, time.Hour}
	defer func() { owedRetryDelays = old }()
	var mu sync.Mutex
	var reminder string
	var sTyped func(string)
	titled := false
	brain := func(_ string, msgs []map[string]any) reply {
		last := lastUserText(msgs)
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.Contains(last, "has had no answer"):
			if reminder == "" {
				reminder = last
			}
		case strings.Contains(allText(msgs), "did the tests pass?") && !titled && msgs[len(msgs)-1]["role"] != "tool":
			// An act that is not an answer: the turn goes on the record,
			// and the second message arrives while it runs.
			titled = true
			sTyped("@e and is the deploy done?")
			time.Sleep(100 * time.Millisecond)
			return reply{calls: []event.ToolCall{tc("update_thread", `{"title":"Tests and deploy"}`)}}
		}
		return hold()
	}
	s := startSession(t, "", brain, nil)
	defer s.stop()
	mu.Lock()
	sTyped = s.type_
	mu.Unlock()
	s.settled()
	s.type_("@e did the tests pass?")
	s.waitFor("the reminder", func() bool { mu.Lock(); defer mu.Unlock(); return reminder != "" })
	mu.Lock()
	got := reminder
	mu.Unlock()
	i, j := strings.Index(got, "did the tests pass?"), strings.Index(got, "and is the deploy done?")
	if i < 0 || j < 0 || i > j {
		t.Fatalf("the reminder does not name both messages, oldest first: %q", got)
	}
}
