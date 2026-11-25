package harness

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
)

// As TestAFallbackIsNotSentAPictureTheRecordSaysWasRefused, but the
// fallback's first answer is sent back (text with no tool call). The
// second try is built from the base the turn rendered before the refusal
// was written, so it reaches the fallback with the picture in it again,
// although the first request to the fallback had the note there.
func TestASentBackCallToTheFallbackIsNotSentTheRefusedPicture(t *testing.T) {
	const fallback = "eagent-fallback"
	var mu sync.Mutex
	looks, uploads := 0, 0
	nudged, said := false, false
	s := startSession(t, "", func(model string, msgs []map[string]any) reply {
		mu.Lock()
		defer mu.Unlock()
		if model == fallback {
			if hasPicturePart(msgs) {
				uploads++
				return reply{status: 400, body: `{"error":{"message":"Could not process image","type":"invalid_request_error"}}`}
			}
			if strings.Contains(allText(msgs), "image from view_image") && !nudged {
				nudged = true
				return reply{text: "I could not see that picture."} // no tool call: sent back
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
	mu.Lock()
	defer mu.Unlock()
	if uploads != 0 {
		t.Errorf("the fallback was sent the refused picture %d times (the sent-back call reuses the base rendered before the refusal)", uploads)
	}
	if got := len(s.events(event.ImagesRefused)); got != 1 {
		t.Errorf("%d refusals on the record, want 1", got)
	}
}
