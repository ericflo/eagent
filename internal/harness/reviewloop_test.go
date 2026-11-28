package harness

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ericflo/eagent/internal/config"
)

// An automatic rollover keeps room in the window for the dossier pass to be
// written inside the conversation (config.rolloverReserve holds back the
// pass's least reading budget for it). A prompt that has just crossed the
// threshold by one ordinary turn must therefore still be written inside
// the conversation, not from the files.
func TestAutoRolloverLeavesRoomToWriteTheDossierInTheConversation(t *testing.T) {
	const window, ceiling = 128_000, 32_768
	limit := config.AutoRollover(window, ceiling)
	var mu sync.Mutex
	var asks []string
	var first atomic.Bool
	s := startSession(t, brief, func(_ string, msgs []map[string]any) reply {
		if ask := dossierRequest(msgs); ask != "" {
			mu.Lock()
			asks = append(asks, ask)
			mu.Unlock()
			return reply{text: briefing(places(ask)[:3])}
		}
		r := hold()
		if first.CompareAndSwap(false, true) {
			r.prompt = limit + 2_000 // one turn past the threshold
		}
		return r
	}, func(cfg *config.Config) {
		cfg.RolloverTokens = 0
		cfg.Model.ContextTokens, cfg.Model.MaxTokens = window, ceiling
	})
	s.waitFor("the dossier", func() bool { return len(s.dossiers()) == 1 })
	mu.Lock()
	defer mu.Unlock()
	if len(asks) == 0 || strings.Contains(asks[0], fromTheFiles) {
		t.Fatalf("a %d-token window rolled over at %d (limit %d) wrote its dossier from the files, without the conversation", window, limit+2_000, limit)
	}
}
