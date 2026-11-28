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
	// Told once that the session is being reread, though two restarts
	// came one after the other, and then answered.
	if got := s.ui.said(); len(got) != 2 || got[0] != rereadNotice || got[1] != "Noted." {
		t.Fatalf("the user heard %q, want to be told the session is being reread and then the answer", got)
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

// r5Scale runs calibrate for a request of the given messages, answered
// with a prompt of input tokens, and returns the ratio the loop holds
// after it.
func r5Scale(s *session, msgs []llm.Message, input int) int {
	req := llm.Request{System: s.rt.system, Messages: msgs}
	var ep llm.Endpoint
	s.rt.sync(func() { ep = s.rt.models[event.ActorEagent].client.Endpoint })
	s.rt.calibrate(event.ActorEagent, ep, req, &llm.Response{Usage: event.Usage{Input: input}}, nil)
	k := 0
	s.rt.sync(func() { k = s.rt.scale() })
	return k
}

// r5Estimate is eagent's estimate of a request of these messages.
func r5Estimate(s *session, msgs []llm.Message) int {
	est := llm.EstimateTokens(s.rt.system) + int(toolTokens())
	for _, m := range msgs {
		est += llm.EstimateTokens(m.Text)
	}
	return est
}

// A picture the record says was refused goes out as a note, so it does not
// keep the request from teaching the ratio; one that goes out still does.
func TestTheProviderRatioIsLearnedPastAPictureThatIsNotSent(t *testing.T) {
	s := startSession(t, "", func(string, []map[string]any) reply { return hold() }, nil)
	shot := filepath.Join(s.rt.opts.Project, "shot.png")
	if err := os.WriteFile(shot, tinyPNG, 0o644); err != nil {
		t.Fatal(err)
	}
	text := strings.Repeat("the heron lands at dusk ", 200)
	refused := []llm.Message{{Role: "user", Text: text, Images: []llm.Image{{Path: shot, Refused: true}}}}
	if k := r5Scale(s, refused, 2*r5Estimate(s, refused)); k < 1900 || k > 2000 {
		t.Fatalf("ratio %d after a request whose only picture was refused, want about 2000", k)
	}
	sent := []llm.Message{{Role: "user", Text: text, Images: []llm.Image{{Path: shot}}}}
	if k := r5Scale(s, sent, 3*r5Estimate(s, sent)); k > 2000 {
		t.Fatalf("ratio %d after a request that sent a picture, want the 2000 learned before", k)
	}
}

// Reasoning replayed verbatim may or may not be counted by the provider. A
// request that carries it never lowers the ratio, and the reasoning's own
// size is not taken for the text's.
func TestReplayedReasoningDoesNotInflateTheProviderRatio(t *testing.T) {
	s := startSession(t, "", func(string, []map[string]any) reply { return hold() }, nil)
	native := json.RawMessage(`[{"type":"reasoning","encrypted_content":"` + strings.Repeat("Zm9v", 6000) + `"}]`)
	msgs := []llm.Message{{Role: "user", Text: "go on"}, {Role: "assistant", Text: "on it", Native: native, NativeProtocol: llm.ProtocolResponses}}
	est, reasoning := r5Estimate(s, msgs), llm.EstimateTokens(string(native))
	// Counted by the provider: the text itself is counted as estimated.
	if k := r5Scale(s, msgs, est+reasoning); k != 1000 {
		t.Fatalf("ratio %d when the provider counted the reasoning and the text as estimated, want 1000", k)
	}
	plain := []llm.Message{{Role: "user", Text: strings.Repeat("go on ", 400)}}
	if k := r5Scale(s, plain, r5Estimate(s, plain)*3/2); k < 1450 || k > 1500 {
		t.Fatalf("ratio %d after a plain request counted at 1.5 times, want about 1500", k)
	}
	// Dropped by the provider: that does not lower what was learned.
	if k := r5Scale(s, msgs, est); k < 1450 || k > 1500 {
		t.Fatalf("ratio %d after a request whose reasoning the provider dropped, want the 1500 learned before", k)
	}
}

// The ratio belongs to the route it was learned on: after a move to
// another route (a fallback, a model picked in the thread's settings) it
// starts at 1 again.
func TestTheProviderRatioIsNotCarriedToAnotherRoute(t *testing.T) {
	s := startSession(t, "", func(string, []map[string]any) reply { return hold() }, nil)
	plain := []llm.Message{{Role: "user", Text: strings.Repeat("go on ", 400)}}
	if k := r5Scale(s, plain, r5Estimate(s, plain)*2); k < 1950 {
		t.Fatalf("ratio %d, want about 2000", k)
	}
	k := 0
	s.rt.sync(func() {
		m := s.rt.models[event.ActorEagent]
		ep := m.client.Endpoint
		ep.Model = "another-model"
		m.client = s.rt.newClient(ep, event.ActorEagent)
		k = s.rt.scale()
	})
	if k != 1000 {
		t.Fatalf("ratio %d on another route, want 1000", k)
	}
}

// A route that sets no reply ceiling still asks for 16,384 tokens of reply
// on every call (llm.Client), and the dossier pass's reading is held to
// what the window has left beside that, as it is on a route that sets one.
func TestTheDossiersReadingLeavesRoomForTheDefaultReply(t *testing.T) {
	for _, ceiling := range []int{0, 16384} {
		s := startSession(t, "", func(string, []map[string]any) reply { return hold() }, func(c *config.Config) {
			c.Model.ContextTokens, c.Model.MaxTokens = 200_000, ceiling
		})
		got := 0
		s.rt.sync(func() { got = s.rt.dossierBudget(100_000, false) })
		if want := (200_000 - 100_000 - 16384 - 8000) * 3 * 4 / 5; got != want {
			t.Errorf("reply ceiling %d: the pass may read %d characters, want %d", ceiling, got, want)
		}
		s.stop()
	}
}

// While the dossier is written the record goes on growing, and the pass's
// lookups read it as it is. A briefing that cites a line written during
// the pass cites a place that exists: it is not sent back for it, and the
// dossier does not say it is wrong.
func TestABriefingMayCiteWhatWasRecordedDuringThePass(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	open := func() { once.Do(func() { close(release) }) }
	defer open()
	var mu sync.Mutex
	cite := ""
	asked := 0
	s := dossierAfterALook(t, nil, func(ask string, msgs []map[string]any) reply {
		mu.Lock()
		asked++
		mu.Unlock()
		<-release
		mu.Lock()
		defer mu.Unlock()
		return reply{text: briefing(append(places(ask)[:3], cite))}
	})
	s.waitFor("the pass", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return asked == 1
	})
	before := len(s.events(event.UserMessage))
	s.type_("@e also bump the linter while you are at it")
	s.waitFor("the message on the record", func() bool { return len(s.events(event.UserMessage)) > before })
	ev := s.events(event.UserMessage)[before]
	mu.Lock()
	cite = fmt.Sprintf("%s:%d", ev.Source.File, ev.Source.Line)
	mu.Unlock()
	open()
	s.waitFor("the dossier", func() bool { return len(s.dossiers()) == 1 })
	d := s.dossiers()[0]
	if strings.Contains(d.Text, "do not exist") {
		t.Fatalf("the dossier says a place recorded during the pass does not exist:\n%s", d.Text)
	}
	if n := r4Logged(s, "sent back once"); n != 0 {
		t.Fatalf("the briefing was sent back %d times", n)
	}
	mu.Lock()
	defer mu.Unlock()
	if asked != 1 {
		t.Fatalf("the model was asked %d times for the briefing, want once", asked)
	}
}
