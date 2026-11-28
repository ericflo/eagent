package harness

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// Finding 10 of the ninth review: a picture the provider refused is not in
// the conversation, so looking at the same file again is not answered with
// "the picture is in your conversation above".
func TestR9APictureThatWasRefusedIsNotSaidToBeInTheConversation(t *testing.T) {
	var mu sync.Mutex
	looks := 0
	var results []string
	s := startSession(t, "", func(_ string, msgs []map[string]any) reply {
		mu.Lock()
		defer mu.Unlock()
		if last := msgs[len(msgs)-1]; last["role"] == "tool" {
			if c, ok := last["content"].(string); ok {
				results = append(results, c)
			}
			return hold()
		}
		switch {
		case hasPicturePart(msgs):
			return reply{status: 400, body: `{"error":{"message":"Could not process image","type":"invalid_request_error"}}`}
		case strings.Contains(lastUserText(msgs), "kept for you") && looks < 2:
			looks++
			return reply{calls: []event.ToolCall{tc("view_image", `{"path":"shot.png"}`)}}
		}
		return hold()
	}, nil)
	if err := os.WriteFile(filepath.Join(s.rt.opts.Project, "shot.png"), tinyPNG, 0o644); err != nil {
		t.Fatal(err)
	}
	s.type_("@e look at shot.png")
	s.waitFor("the refusal on the record", func() bool { return len(s.events(event.ImagesRefused)) == 1 })
	s.settled()
	s.type_("@e look at it again")
	s.waitFor("the second look", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return looks == 2 && len(results) >= 2
	})
	s.settled()
	mu.Lock()
	defer mu.Unlock()
	for _, r := range results {
		if strings.Contains(r, "in your conversation above") {
			t.Fatalf("the second look says the refused picture is in the conversation: %q", r)
		}
	}
}
