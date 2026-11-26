package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/llm"
	"github.com/ericflo/eagent/internal/tools"
)

// r5PictureRoute is a small local route whose provider counts each picture
// in a request as picture tokens, far more than eagent can know from the
// record, and refuses what is past the window. refuseAll, once set,
// refuses every request but a dossier pass.
type r5PictureRoute struct {
	window, ceiling int
	picture         int
	refuseAll       atomic.Bool
	mu              sync.Mutex
	passes          int
}

func (rt *r5PictureRoute) brain() func(string, []map[string]any) reply {
	b, _ := json.Marshal(tools.All())
	toolTokens := llm.EstimateTokens(string(b))
	tooLong := reply{status: 400, body: `{"error":{"message":"prompt is too long for the model's context window","type":"invalid_request_error"}}`}
	return func(_ string, m []map[string]any) reply {
		raw, _ := json.Marshal(m)
		pictures := 0
		for _, msg := range m {
			parts, _ := msg["content"].([]any)
			for _, p := range parts {
				if pm, ok := p.(map[string]any); ok && pm["type"] == "image_url" {
					pictures++
				}
			}
		}
		n := llm.EstimateTokens(string(raw)) + toolTokens + pictures*rt.picture
		rt.mu.Lock()
		defer rt.mu.Unlock()
		if ask := dossierRequest(m); ask != "" {
			if n > rt.window-rt.ceiling {
				return tooLong
			}
			rt.passes++
			p := places(ask)
			return reply{text: briefing(p[max(0, len(p)-3):]), prompt: n}
		}
		if n > rt.window-rt.ceiling || rt.refuseAll.Load() {
			return tooLong
		}
		r := hold()
		if strings.Contains(lastUserText(m), "kept for you") && m[len(m)-1]["role"] != "tool" {
			r = say("Noted.")
		}
		r.prompt = n
		return r
	}
}

// r5SendPicture records a message to eagent with a picture attached, as the
// phone or the web page sends one.
func r5SendPicture(s *session, text string) {
	shot := filepath.Join(s.rt.opts.Project, "shot.png")
	if err := os.WriteFile(shot, tinyPNG, 0o644); err != nil {
		s.t.Fatal(err)
	}
	s.rt.sync(func() {
		s.rt.userMessage(event.UserMessageData{
			Text:        text,
			Route:       "eagent",
			Attachments: []event.Attachment{{Name: "shot.png", Path: shot, ContentType: "image/png", Kind: "image", Size: int64(len(tinyPNG))}},
		})
	})
}

// On a small route a message with a picture the provider counts as more
// than the window holds is refused. The restart that follows carries the
// message whole (by eagent's count it fits), and the new conversation's
// first call is refused again. The next restart takes the message in by
// place instead of carrying it, so its conversation fits and the user is
// answered: two restarts, never one after another for good.
func TestAPictureThatCannotFitDoesNotRestartTheConversationForever(t *testing.T) {
	route := &r5PictureRoute{window: 60000, ceiling: 2048, picture: 80000}
	s := startSession(t, "", route.brain(), func(c *config.Config) {
		c.RolloverTokens = 0
		c.Model.ContextTokens, c.Model.MaxTokens = route.window, route.ceiling
	})
	r5LogOnFailure(s)
	s.settled()
	r5SendPicture(s, "@e what does this screenshot show?")
	r4Until(t, "the answer, or a restart loop", func() bool {
		return s.ui.messageCount() > 0 || len(s.events(event.Dossier)) > 4
	})
	r4Settled(s)
	if n := len(s.events(event.Dossier)); n > 2 {
		t.Fatalf("%d restarts for one message with a picture, want at most 2", n)
	}
	if got := s.ui.said(); len(got) != 1 || got[0] != "Noted." {
		t.Fatalf("the user heard %q, want the answer", got)
	}
	// The message is still reachable from the newest briefing by its place.
	ds := s.dossiers()
	if last := ds[len(ds)-1].Text; !strings.Contains(last, "what does this screenshot show?") {
		t.Fatalf("the newest briefing does not hold the message it took in:\n%s", last)
	}
}

// When even a restart that carries nothing is refused on its first call,
// no restart can make room. That is said once, and the loop stops
// restarting, however many times it looks again.
func TestARestartThatCannotMakeRoomForAPictureSaysSoOnceAndStops(t *testing.T) {
	old := errorRetryDelays
	errorRetryDelays = []time.Duration{5 * time.Millisecond}
	t.Cleanup(func() { errorRetryDelays = old })
	for _, window := range []int{16384, 60000} {
		t.Run(fmt.Sprint(window), func(t *testing.T) {
			route := &r5PictureRoute{window: window, ceiling: 2048, picture: 80000}
			s := startSession(t, "", route.brain(), func(c *config.Config) {
				c.RolloverTokens = 0
				c.Model.ContextTokens, c.Model.MaxTokens = route.window, route.ceiling
			})
			r5LogOnFailure(s)
			s.settled()
			route.refuseAll.Store(true)
			r5SendPicture(s, "@e what does this screenshot show?")
			r4Until(t, "the user to be told no restart can make room, or a restart loop", func() bool {
				return r5Told(s, fullNotice) > 0 || len(s.events(event.Dossier)) > 4
			})
			// Many more looks: the refusals are retried every few
			// milliseconds, and none of them may start another restart.
			calls := s.model.count(testModel)
			r4Until(t, "more refused calls", func() bool { return s.model.count(testModel) > calls+20 })
			if n := len(s.events(event.Dossier)); n > 2 {
				t.Fatalf("%d restarts, want at most 2", n)
			}
			if n := r5Told(s, fullNotice); n != 1 {
				t.Fatalf("the user was told %d times that no restart can make room, want once", n)
			}
			if n := r4Logged(s, "restarting again cannot shrink it") + r4Logged(s, "no restart can make room"); n != 1 {
				t.Fatalf("the log says %d times that no restart can make room, want once", n)
			}
		})
	}
}

// r5Told counts what the user was told that begins with prefix.
func r5Told(s *session, prefix string) int {
	n := 0
	for _, m := range s.ui.said() {
		if strings.HasPrefix(m, prefix) {
			n++
		}
	}
	return n
}

// r5LogOnFailure prints what the session logged about its restarts when the
// test fails.
func r5LogOnFailure(s *session) {
	s.t.Cleanup(func() {
		if !s.t.Failed() {
			return
		}
		for _, l := range s.ui.logged() {
			if strings.Contains(l, "restart") || strings.Contains(l, "dossier") || strings.Contains(l, "too long") || strings.Contains(l, "subsession") || strings.Contains(l, "room") {
				s.t.Log(l)
			}
		}
	})
}
