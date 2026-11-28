package harness

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
)

// The route refuses a picture and then, on the retry without it, turns out
// to be unavailable (no credits), so the call moves to the fallback route.
// The refusal is on the record by then, and the request handed to the
// fallback already has the note in the picture's place: the fallback is
// not sent the picture, and what it is sent for that message is what every
// later request renders for it.
func TestAFallbackIsNotSentAPictureTheRecordSaysWasRefused(t *testing.T) {
	const fallback = "eagent-fallback"
	var mu sync.Mutex
	looks, uploads := 0, 0
	said := false
	s := startSession(t, "", func(model string, msgs []map[string]any) reply {
		mu.Lock()
		defer mu.Unlock()
		if model == fallback {
			if hasPicturePart(msgs) {
				uploads++
				return reply{status: 400, body: `{"error":{"message":"Could not process image","type":"invalid_request_error"}}`}
			}
			if strings.Contains(allText(msgs), "image from view_image") && !said {
				said = true
				return say("I could not see that picture.")
			}
			return hold()
		}
		switch {
		case hasPicturePart(msgs):
			return reply{status: 400, body: `{"error":{"message":"Could not process image","type":"invalid_request_error"}}`}
		case phoneAfterLook(msgs):
			return reply{status: 402, body: `{"error":{"message":"Insufficient credits","code":402}}`}
		case strings.Contains(lastUserText(msgs), "kept for you") && looks == 0:
			looks++
			return reply{calls: []event.ToolCall{tc("view_image", `{"path":"shot.png"}`)}}
		}
		return hold()
	}, func(cfg *config.Config) {
		cfg.Model.Fallback = &config.ModelRoute{Protocol: cfg.Model.Protocol, BaseURL: cfg.Model.BaseURL, Model: fallback, APIKeyEnv: cfg.Model.APIKeyEnv}
	})
	if err := os.WriteFile(filepath.Join(s.rt.opts.Project, "shot.png"), tinyPNG, 0o644); err != nil {
		t.Fatal(err)
	}
	s.type_("@e look at shot.png")
	s.waitFor("the answer from the fallback", func() bool { return s.ui.messageCount() == 1 })
	s.settled()
	s.type_("@e and now?")
	s.waitFor("a second call to the fallback", func() bool {
		s.model.mu.Lock()
		defer s.model.mu.Unlock()
		return s.model.calls[fallback] >= 3
	})
	s.settled()
	if got := len(s.events(event.ImagesRefused)); got != 1 {
		t.Fatalf("%d refusals on the record, want 1", got)
	}
	if got := len(s.events(event.Route)); got != 1 {
		t.Fatalf("%d route changes, want the one to the fallback", got)
	}
	mu.Lock()
	if uploads != 0 {
		t.Fatalf("the fallback was sent the refused picture %d times", uploads)
	}
	mu.Unlock()
	// The message that held the picture, as each request to the fallback
	// rendered it: the same bytes every time.
	s.model.mu.Lock()
	defer s.model.mu.Unlock()
	var seen [][]byte
	for _, req := range s.model.reqs {
		if req.Model != fallback {
			continue
		}
		for _, m := range req.Messages {
			if bytes.Contains(m, []byte("image from view_image")) {
				seen = append(seen, m)
			}
		}
	}
	if len(seen) < 2 {
		t.Fatalf("the fallback saw the picture's message in %d requests, want at least 2", len(seen))
	}
	for i, m := range seen {
		if !bytes.Contains(m, []byte("could not be shown to this model")) {
			t.Fatalf("request %d to the fallback has no note for the picture: %s", i+1, m)
		}
		if !bytes.Equal(m, seen[0]) {
			t.Fatalf("the picture's message changed between requests to the fallback:\n first %s\n later %s", seen[0], m)
		}
	}
}
