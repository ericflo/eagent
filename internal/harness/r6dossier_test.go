package harness

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/tools"
)

// r6Excerpt is a log excerpt of about 82 KiB that the user pastes to
// eagent, the i-th of a burst: one line of the record, ending in a mark
// that says which it is. The lines are built once.
func r6Excerpt(i int) string {
	return fmt.Sprintf("@e excerpt %d of the build log:\n%sEND-OF-EXCERPT-%d", i, r6LogLines(), i)
}

var r6LogLines = sync.OnceValue(func() string {
	var b strings.Builder
	for b.Len() < 82*1024 {
		fmt.Fprintf(&b, "2026-11-25T10:%02d:%02d worker step %d ok: compiled internal/harness in 1.9s\n", b.Len()%60, b.Len()%59, b.Len())
	}
	return b.String()
})

// r6PictureTokens is what the fake provider counts for one picture: what a
// local vision model can count for a large one. eagent cannot see it (its
// estimate counts a picture by its path) and does not learn its ratio from
// a request that sends one (calibrate).
const r6PictureTokens = 16_000

// r6Count is the fake provider's count of a request: ratio (in hundredths)
// times eagent's estimate of everything it sends, the system prompt and
// the tools included, and each picture as r6PictureTokens. It counts what
// is sent and nothing else, evenly. It reads each text once and marshals
// nothing, so a request of half a megabyte costs one pass over it.
func r6Count(m []map[string]any, ratio int) int {
	n := toolTokens()
	pictures := 0
	text := func(s string) {
		n += len(s)*2/7 + 1
	}
	for _, msg := range m {
		n += 4
		switch c := msg["content"].(type) {
		case string:
			text(c)
		case []any:
			for _, p := range c {
				pm, _ := p.(map[string]any)
				if pm["type"] == "image_url" {
					pictures++
				} else if s, ok := pm["text"].(string); ok {
					text(s)
				}
			}
		}
		if calls, ok := msg["tool_calls"].([]any); ok {
			n += 50 * len(calls)
		}
	}
	return n*ratio/100 + pictures*r6PictureTokens
}

// r6Brain answers as a route of this window whose provider counts with
// r6Count at ratio and refuses what is past the window: a dossier pass
// gets a briefing, a look at a message kept for eagent gets "Noted.", any
// other look holds.
func r6Brain(window, ceiling, ratio int) func(string, []map[string]any) reply {
	tooLong := reply{status: 400, body: `{"error":{"message":"prompt is too long for the model's context window","type":"invalid_request_error"}}`}
	return func(_ string, m []map[string]any) reply {
		n := r6Count(m, ratio)
		if n > window-ceiling {
			return tooLong
		}
		if ask := dossierRequest(m); ask != "" {
			p := places(ask)
			return reply{text: briefing(p[max(0, len(p)-3):]), prompt: n}
		}
		r := hold()
		if strings.Contains(lastUserText(m), "kept for you") && m[len(m)-1]["role"] != "tool" {
			r = say("Noted.")
		}
		r.prompt = n
		return r
	}
}

// r6Burst is how many 82 KiB excerpts are more than a route of this window
// can take in one request.
func r6Burst(window int) int {
	return window/r6ExcerptTokens + 1
}

// r6ExcerptTokens is what eagent's estimate makes of one 82 KiB excerpt.
const r6ExcerptTokens = 82 * 1024 * 2 / 7

// r6Pictures is how many pictures are more than a route of this window
// can take in one request.
func r6Pictures(window int) int {
	return window/r6PictureTokens + 1
}

// r6QuietRestart runs a session on a route of the given window, whose
// provider counts as eagent estimates and refuses what is past the window.
// The user's first message carries pictures enough to put its look past
// the restart size, so the conversation is restarted at the next quiet
// moment with nothing unseen: that restart carries nothing. (Pictures,
// because a request that sends one teaches no token ratio: the fake never
// reports a size the estimate could be compared with and found wrong.) While its pass is held open, during(s) runs; then the
// pass is let go. passes > 1 are held too when hold says so. It returns
// once the user's last message is answered, the user is told the
// conversation is full, or there are more restarts than any rule allows.
func r6QuietRestart(t *testing.T, window int, during func(s *session, pass int)) (*session, *r5PictureRoute) {
	return r6Restart(t, window, nil, during)
}

// r6Restart is r6QuietRestart, with first run while the first answer is
// out: what it records is unseen when the restart begins, right after.
func r6Restart(t *testing.T, window int, first func(s *session), during func(s *session, pass int)) (*session, *r5PictureRoute) {
	route := &r5PictureRoute{window: window, ceiling: 16384, picture: r6PictureTokens}
	inner := r6Brain(route.window, route.ceiling, 100)
	var passes, answers atomic.Int32
	held := make(chan int, 8)
	release := make(chan struct{}, 8)
	var s *session
	brain := func(model string, m []map[string]any) reply {
		if dossierRequest(m) != "" {
			n := int(passes.Add(1))
			held <- n
			<-release
			return inner(model, m)
		}
		r := inner(model, m)
		if r.status == 0 && answers.Add(1) == 1 && first != nil {
			first(s)
		}
		return r
	}
	s = r4StartSession(t, "", brain, func(c *config.Config) {
		c.RolloverTokens = 0
		c.Model.ContextTokens, c.Model.MaxTokens = route.window, route.ceiling
	})
	t.Cleanup(func() {
		for i := 0; i < 8; i++ {
			release <- struct{}{}
		}
	})
	r5LogOnFailure(s)
	r4Settled(s)
	limit := 0
	s.rt.sync(func() { limit = s.rt.rolloverTokens() })
	r6SendEach(s, 1, func(int) event.UserMessageData {
		return r6WithPictures("@e hello, here is how it looks", limit/r6PictureTokens+1, s)
	})
	go func() {
		for n := range held {
			during(s, n)
			release <- struct{}{}
		}
	}()
	r6Until(t, "the answer, the conversation said to be full, or too many restarts", func() bool {
		return r5Told(s, "Noted.") >= 2 || r6ToldFull(s) > 0 || r6Dossiers(s) > 4
	})
	return s, route
}

// r6Until waits for cond, looking every 10 ms. What it looks at is the
// fake UI and a count kept off the loop, so looking does not make the loop
// run its scheduler (which, with half a megabyte unseen, reads it again).
func r6Until(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// r6Dossiers counts the dossiers written, from the log line each one gets,
// without asking the loop.
func r6Dossiers(s *session) int { return r4Logged(s, "begins with a dossier written by") }

// r6ToldFull counts what the user was told that says no restart can make
// room.
func r6ToldFull(s *session) int {
	n := 0
	for _, m := range s.ui.said() {
		if strings.HasPrefix(m, fullNotice) || strings.HasPrefix(m, "My conversation is full") {
			n++
		}
	}
	return n
}

// r6SendBurst sends n excerpts of 82 KiB to eagent and returns their
// events.
func r6SendBurst(s *session, n int) []event.Event {
	return r6Send(s, n, r6Excerpt)
}

func r6Send(s *session, n int, excerpt func(int) string) []event.Event {
	return r6SendEach(s, n, func(i int) event.UserMessageData {
		return event.UserMessageData{Text: excerpt(i), Route: "eagent"}
	})
}

// r6SendPictures sends a burst of pictures to eagent, more than the
// window takes in one request: six messages, the pictures shared out
// among them.
func r6SendPictures(s *session, window int) []event.Event {
	per := (r6Pictures(window) + 5) / 6
	return r6SendEach(s, 6, func(i int) event.UserMessageData {
		return r6WithPictures(fmt.Sprintf("@e screenshots, set %d", i), per, s)
	})
}

// r6WithPictures is a message to eagent with k pictures attached.
func r6WithPictures(text string, k int, s *session) event.UserMessageData {
	shot := filepath.Join(s.rt.opts.Project, "shot.png")
	if err := os.WriteFile(shot, tinyPNG, 0o644); err != nil {
		s.t.Fatal(err)
	}
	d := event.UserMessageData{Text: text, Route: "eagent"}
	for j := 0; j < k; j++ {
		d.Attachments = append(d.Attachments, event.Attachment{Name: "shot.png", Path: shot, ContentType: "image/png", Kind: "image", Size: int64(len(tinyPNG))})
	}
	return d
}

// r6SendEach records the n messages and returns their events.
func r6SendEach(s *session, n int, msg func(int) event.UserMessageData) []event.Event {
	var out []event.Event
	for i := 1; i <= n; i++ {
		d := msg(i)
		s.rt.sync(func() {
			s.rt.userMessage(d)
			for j := len(s.rt.st.Events) - 1; j >= 0; j-- {
				if ev := s.rt.st.Events[j]; ev.Type == event.UserMessage {
					out = append(out, ev)
					break
				}
			}
		})
	}
	return out
}

// r6Reachable checks that the newest dossier tells the model plainly that
// these messages were not seen, with each one's place, and that each can be
// read whole from there with session_read.
func r6Reachable(t *testing.T, s *session, sent []event.Event) {
	t.Helper()
	ds := s.dossiers()
	brief := ds[len(ds)-1].Text
	at := strings.Index(brief, unseenHeading)
	if at < 0 {
		t.Fatalf("the newest dossier does not say the user sent things it has not seen:\n%.3000s", brief)
	}
	block := brief[at:]
	if end := strings.Index(block, "\n\n"); end > 0 {
		block = block[:end]
	}
	// The model reads it as the first thing of the conversation it is
	// given, not only in the record.
	opening := ""
	s.rt.sync(func() {
		if v := s.rt.st.View(nil); len(v) > 0 {
			opening = v[0].Text
		}
	})
	if !strings.Contains(opening, unseenHeading) {
		t.Fatalf("the conversation's opening does not say what was not seen:\n%.2000s", opening)
	}
	count := fmt.Sprintf("%d messages", len(sent))
	if len(sent) == 1 {
		count = "1 message"
	}
	if !strings.Contains(block, count) {
		t.Fatalf("the dossier's note does not say how many messages were not seen:\n%s", block)
	}
	read := func(args string) string {
		out, isErr, _ := s.rt.runTool(context.Background(), &turnCalls{}, tc("session_read", args))
		if isErr {
			t.Fatalf("%s: %s", args, out)
		}
		return out
	}
	for i, ev := range sent {
		place := fmt.Sprintf("%s:%d", ev.Source.File, ev.Source.Line)
		if !strings.Contains(block, place) {
			t.Fatalf("the dossier's note does not give the place of excerpt %d (%s):\n%s", i+1, place, block)
		}
		var d event.UserMessageData
		_ = ev.Decode(&d)
		if !strings.Contains(d.Text, "END-OF-EXCERPT-") {
			// A picture: the note names the file to look at, and the raw
			// line holds it.
			path := d.Attachments[0].Path
			if !strings.Contains(block, path) {
				t.Fatalf("the dossier's note does not name the picture of message %d (%s):\n%s", i+1, path, block)
			}
			got := read(fmt.Sprintf(`{"file":%q,"from":%d,"to":%d,"raw":true}`, ev.Source.File, ev.Source.Line, ev.Source.Line))
			if !strings.Contains(got, "shot.png") || !strings.Contains(got, d.Text) {
				t.Fatalf("message %d at %s does not hold its picture: %.300s", i+1, place, got)
			}
			continue
		}
		head, end := fmt.Sprintf("excerpt %d of the build log", i+1), fmt.Sprintf("END-OF-EXCERPT-%d", i+1)
		if i == 0 {
			// The first is read whole, piece by piece, as the model
			// would.
			got := r2Follow(t, read, ev.Source.File, ev.Source.Line, false, loopReadChars+400)
			if !strings.Contains(got, end) || !strings.Contains(got, head) {
				t.Fatalf("excerpt %d cannot be read whole from %s (%d characters)", i+1, place, len(got))
			}
			continue
		}
		// The others the same way, at their first piece and their last:
		// the pieces between are the same reads as the first one's.
		first := read(fmt.Sprintf(`{"file":%q,"from":%d,"to":%d}`, ev.Source.File, ev.Source.Line, ev.Source.Line))
		total := len(tools.Summarize(ev, 0))
		last := read(fmt.Sprintf(`{"file":%q,"from":%d,"offset":%d}`, ev.Source.File, ev.Source.Line, max(0, total-200)))
		if !strings.Contains(first, head) || !strings.Contains(last, end) {
			t.Fatalf("excerpt %d cannot be read from %s at its start and its end:\n%.300s\n...\n%.300s", i+1, place, first, last)
		}
	}
}

// A quiet restart carries nothing; while its dossier is written the user
// pastes a burst of log excerpts, more than the window holds. The first
// call of the new conversation, which holds them whole, is refused. On
// every size of route that is the same case: one more restart lists them
// by place, the user is answered and told nothing alarming, and the new
// briefing says plainly what was not seen and where it is. (The same on a
// small route is TestAPictureSentDuringAPassThatCarriedNothingGetsOneMoreRestart
// and TestALongMessageSentDuringAPassThatCarriedNothingGetsOneMoreRestart.)
// On the 200k route the burst is of pictures, which the provider counts
// as many tokens and the record holds as paths.
func r6BurstOnEveryRouteSize(t *testing.T) {
	for _, window := range []int{128_000, 200_000, 1_000_000} {
		t.Run(fmt.Sprint(window), func(t *testing.T) {
			t.Parallel()
			var sent []event.Event
			s, _ := r6QuietRestart(t, window, func(s *session, pass int) {
				if pass != 1 {
					return
				}
				switch window {
				case 128_000:
					// The reviewer's case as it was: six excerpts of
					// 82 KiB, whole in the record and in the request.
					sent = r6SendBurst(s, r6Burst(window))
				default:
					// Pictures, which cost the record a path and the
					// provider many tokens.
					sent = r6SendPictures(s, window)
				}
			})
			if n := r6ToldFull(s); n != 0 {
				t.Fatalf("the user was told the conversation is full, and one more restart fits")
			}
			if n := len(s.events(event.Dossier)); n != 2 {
				t.Fatalf("%d restarts, want 2: the quiet one and the one more try", n)
			}
			if n := r5Told(s, "Noted."); n < 2 {
				t.Fatalf("the user was not answered after the burst")
			}
			r6Reachable(t, s, sent)
		})
	}
}

// One excerpt during the pass fits beside the briefing: the first call goes
// through, and no second restart is made.
func r6OneExcerptFits(t *testing.T) {
	s, _ := r6QuietRestart(t, 128_000, func(s *session, pass int) {
		if pass == 1 {
			r6SendBurst(s, 1)
		}
	})
	if n := len(s.events(event.Dossier)); n != 1 {
		t.Fatalf("%d restarts, want the quiet one only", n)
	}
	if n := r6ToldFull(s); n != 0 {
		t.Fatal("the user was told the conversation is full")
	}
}

// The burst arrives just before the pass begins, while the first answer is
// out. It is more than a restart carries whole, so the restart lists it by
// place from the start: one restart, the user answered, the briefing says
// where each excerpt is.
func r6BurstJustBefore(t *testing.T) {
	var sent []event.Event
	// One whole excerpt is already past what a restart carries whole
	// (80,000 bytes); six would be given by place the same way.
	s, _ := r6Restart(t, 128_000, func(s *session) { sent = r6SendBurst(s, 1) }, func(*session, int) {})
	if n := len(s.events(event.Dossier)); n != 1 {
		t.Fatalf("%d restarts, want 1", n)
	}
	if n := r6ToldFull(s); n != 0 {
		t.Fatal("the user was told the conversation is full")
	}
	r6Reachable(t, s, sent)
}

// Pictures just before the pass: small in the record, so the restart
// carries them whole, and the provider counts them as more than the window.
// The restart after it carries nothing and gives them by place.
func r6PicturesJustBefore(t *testing.T) {
	s, _ := r6Restart(t, 128_000, func(s *session) { r6SendPictures(s, 128_000) }, func(*session, int) {})
	if n := len(s.events(event.Dossier)); n != 2 {
		t.Fatalf("%d restarts, want 2: one that carried the pictures, one that gave them by place", n)
	}
	if n := r6ToldFull(s); n != 0 {
		t.Fatal("the user was told the conversation is full")
	}
	if ds := s.dossiers(); !strings.Contains(ds[1].Text, unseenHeading) || !strings.Contains(ds[1].Text, "6 messages") {
		t.Fatalf("the second briefing does not say the six screenshots were not seen:\n%.2000s", ds[1].Text)
	}
}

// The burst keeps coming during the second pass as well. That restart was
// the one more try, so it is the last: the user is told once, in words that
// say what happened (not that the notes and instructions are too large),
// and their messages stay in the record, by place.
func r6BurstDuringBothPasses(t *testing.T) {
	var last []event.Event
	s, _ := r6QuietRestart(t, 128_000, func(s *session, pass int) {
		if pass <= 2 {
			last = r6SendPictures(s, 128_000)
		}
	})
	// A few more refused calls (retried every millisecond here), and no
	// restart among them.
	calls := s.model.count(testModel)
	r4Until(t, "more refused calls", func() bool { return s.model.count(testModel) > calls+4 })
	if n := len(s.events(event.Dossier)); n != 2 {
		t.Fatalf("%d restarts, want 2: the quiet one and the one more try", n)
	}
	if n := r6ToldFull(s); n != 1 {
		t.Fatalf("the user was told %d times that the conversation is full, want once", n)
	}
	for _, m := range s.ui.said() {
		if strings.HasPrefix(m, fullNotice) {
			if strings.Contains(m, "standing instructions alone") || !strings.Contains(m, "while I was rereading") {
				t.Fatalf("the user was told the wrong cause: %s", m)
			}
		}
	}
	_ = last
}

// A stop and resume in the middle: the quiet restart's first call is
// refused on the burst, and the session is stopped before anything else.
// The resumed run does not know what that restart carried; it tries once
// more, which lists the burst by place, and the user is answered.
func TestABurstRefusedThenAResumeGetsOneMoreRestart(t *testing.T) {
	setFor(t, &errorRetryDelays, []time.Duration{time.Hour})
	route := &r5PictureRoute{window: 128_000, ceiling: 16384, picture: r6PictureTokens}
	inner := r6Brain(route.window, route.ceiling, 100)
	var passes, refused atomic.Int32
	release := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	inPass := make(chan struct{}, 4)
	brain := func(model string, m []map[string]any) reply {
		if dossierRequest(m) != "" {
			if passes.Add(1) == 1 {
				inPass <- struct{}{}
				<-release
			}
			return inner(model, m)
		}
		r := inner(model, m)
		if r.status != 0 {
			refused.Add(1)
		}
		return r
	}
	tune := func(c *config.Config) {
		c.RolloverTokens = 0
		c.Model.ContextTokens, c.Model.MaxTokens = route.window, route.ceiling
	}
	s := startSession(t, "", brain, tune)
	r5LogOnFailure(s)
	r4Settled(s)
	// Pictures enough to put the first look past the restart size: the
	// next quiet moment restarts.
	limit := 0
	s.rt.sync(func() { limit = s.rt.rolloverTokens() })
	r6SendEach(s, 1, func(int) event.UserMessageData {
		return r6WithPictures("@e hello, here is how it looks", limit/r6PictureTokens+1, s)
	})
	<-inPass
	sent := r6SendPictures(s, 128_000)
	close(release)
	r4Until(t, "the refused first call after the restart", func() bool { return refused.Load() >= 1 })
	cfg, path, project := s.rt.cfg, s.rt.sess.Path, s.rt.opts.Project
	before := len(s.events(event.Dossier))
	s.stop()
	if before != 1 {
		t.Fatalf("%d restarts before the stop, want 1", before)
	}

	s2 := audit2Resume(t, cfg, Options{Project: project, Interactive: true}, path, s.seat, s.model)
	r5LogOnFailure(s2)
	r4Until(t, "the answer after the resume", func() bool { return r5Told(s2, "Noted.") >= 1 || r6ToldFull(s2) > 0 })
	if n := r6ToldFull(s2); n != 0 {
		t.Fatal("the user was told the conversation is full after the resume")
	}
	if n := len(s2.events(event.Dossier)); n != 2 {
		t.Fatalf("%d restarts in all, want 2", n)
	}
	r6Reachable(t, s2, sent)
}

// A burst during the pass that the window takes, but that puts the first
// call past the restart size: nothing was refused, the model saw it and
// answered. The opening itself leaves room, so the conversation is
// restarted again at the next look, as any conversation past the size is,
// and the user is not told the notes are too large.
func r6BurstPastTheSizeInsideTheWindow(t *testing.T) {
	const window = 128_000
	s, _ := r6QuietRestart(t, window, func(s *session, pass int) {
		if pass == 1 {
			// Three whole excerpts, about 72k tokens: past the 64k
			// restart size, inside the window.
			r6SendBurst(s, 3)
		}
	})
	if n := r5Told(s, "Noted."); n < 2 {
		t.Fatal("the burst was not answered")
	}
	r4Until(t, "the next restart", func() bool { return len(s.events(event.Dossier)) == 2 || r6ToldFull(s) > 0 })
	if n := r6ToldFull(s); n != 0 {
		t.Fatal("the user was told the notes and instructions are too large, and the opening leaves room")
	}
}

// r6Instructions is about n tokens (by the estimate) of a project's
// standing instructions.
func r6Instructions(n int) string {
	line := "Always run make check before a commit, and keep the work on main with no branches.\n"
	return strings.Repeat(line, n*7/2/len(line)+1)
}

// An ordinary route (200k window, restart size 100k) whose provider counts
// 1.45 tokens for each one eagent estimates, evenly, as code, logs and
// lists of places can on real tokenizers, with about 62k tokens of standing
// instructions. Every conversation a restart opens is past the restart size
// on its first call, which goes through (it is inside the window). No
// restart can make room: the user is told so once, and the restarts stop.
func r6ProviderCountsMore(t *testing.T) {
	const window, ceiling = 200_000, 16384
	route := &r5PictureRoute{window: window, ceiling: ceiling, picture: 30000}
	inner := route.brain()
	brain := func(model string, m []map[string]any) reply {
		r := inner(model, m)
		if dossierRequest(m) != "" || r.status != 0 {
			return r
		}
		r.prompt = r.prompt * 145 / 100
		if r.prompt > window-ceiling {
			return reply{status: 400, body: `{"error":{"message":"prompt is too long for the model's context window","type":"invalid_request_error"}}`}
		}
		return r
	}
	s := r4StartSession(t, "", brain, func(c *config.Config) {
		c.RolloverTokens = 0
		c.Model.ContextTokens, c.Model.MaxTokens = window, ceiling
		c.Instructions = r6Instructions(62_000)
	})
	r5LogOnFailure(s)
	r4Settled(s)
	var limit int
	var small bool
	s.rt.sync(func() {
		limit = s.rt.rolloverTokens()
		_, small = s.rt.room()
	})
	if small || limit != 100_000 {
		t.Fatalf("the setup is not the ordinary 200k route it means to be")
	}
	for i := 0; i < 6; i++ {
		before := len(s.events(event.Dossier))
		s.rt.sync(func() {
			s.rt.userMessage(event.UserMessageData{Text: fmt.Sprintf("@e status %d?", i), Route: "eagent"})
		})
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) && len(s.events(event.Dossier)) == before && r6ToldFull(s) == 0 {
			time.Sleep(2 * time.Millisecond)
		}
		if r6ToldFull(s) > 0 {
			break
		}
		r4Settled(s)
	}
	n := len(s.events(event.Dossier))
	if n > 3 {
		t.Fatalf("%d restarts for 6 short messages: each conversation's first call is past the restart size in the provider's count", n)
	}
	if told := r6ToldFull(s); told != 1 {
		t.Fatalf("the user was told %d times that no restart can make room, want once", told)
	}
}

// Restarts by how the provider counts. Each case is a route, a provider
// that counts ratio times eagent's estimate of everything sent (evenly,
// the opening included), and standing instructions of a given size. The
// user sends four short messages; with a small opening the first comes
// with pictures enough to put its look past the restart size. With a
// small opening every provider gets one restart and every message an
// answer. With an opening near the restart size, a provider whose count
// puts the opening itself past the size gets one restart and the user is
// told once that no restart can make room; one whose count leaves it
// under the size needs no restart.
func r6ByHowTheProviderCounts(t *testing.T) {
	for _, c := range []struct {
		window, rollover, ratio, instructions int
		restarts, told                        int
	}{
		{60_000, 0, 100, 0, 1, 0},
		{60_000, 0, 145, 0, 1, 0},
		{200_000, 0, 100, 0, 1, 0},
		{200_000, 0, 130, 0, 1, 0},
		{200_000, 0, 145, 0, 1, 0},
		{200_000, 0, 200, 0, 1, 0},
		// Near the restart size: under it at 1.0 and 1.3 (no restart
		// is due), past it at 1.45 and 2 (one restart, then told).
		{200_000, 0, 100, 62_000, 0, 0},
		{200_000, 0, 130, 62_000, 0, 0},
		{200_000, 0, 145, 62_000, 1, 1},
		{200_000, 0, 200, 62_000, 1, 1},
		// On 128k the same opening leaves under 64k of room, so the
		// route counts as small, its restart size follows the
		// provider's count, and the opening is under it.
		{128_000, 0, 145, 62_000, 0, 0},
		{1_000_000, 100_000, 145, 62_000, 1, 1},
	} {
		name := fmt.Sprintf("%dk-rollover%d-x%.2f-instructions%dk", c.window/1000, c.rollover, float64(c.ratio)/100, c.instructions/1000)
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := r4StartSession(t, "", r6Brain(c.window, 16384, c.ratio), func(cfg *config.Config) {
				cfg.RolloverTokens = c.rollover
				cfg.Model.ContextTokens, cfg.Model.MaxTokens = c.window, 16384
				if c.instructions > 0 {
					cfg.Instructions = r6Instructions(c.instructions)
				}
			})
			r5LogOnFailure(s)
			limit := 0
			s.rt.sync(func() { limit = s.rt.rolloverTokens() })
			progress := func() int {
				return r5Told(s, "Noted.") + r5Told(s, stillFullNotice) + r6ToldFull(s)
			}
			send := func(d event.UserMessageData) {
				before := progress()
				s.rt.sync(func() { s.rt.userMessage(d) })
				r6Until(t, "an answer or a notice", func() bool { return progress() > before })
			}
			if c.instructions == 0 {
				// A small opening is past the restart size only with
				// something large: pictures, which teach no ratio.
				send(r6WithPictures("@e hello, here is how it looks", limit/r6PictureTokens+1, s))
			}
			for i := 0; i < 4; i++ {
				send(event.UserMessageData{Text: fmt.Sprintf("@e status %d?", i), Route: "eagent"})
			}
			restarts, told := r6Dossiers(s), r6ToldFull(s)
			t.Logf("%s: %d restarts, told full %d times", name, restarts, told)
			if restarts != c.restarts || told != c.told {
				t.Fatalf("%d restarts and told %d times, want %d and %d", restarts, told, c.restarts, c.told)
			}
		})
	}
}

// Pictures arrive during every pass, each time inside the window but
// enough to put the new conversation's first call past the restart size.
// Each opening leaves room, so each time another restart is worth trying,
// but not for ever: after three restarts in a row whose conversations
// were too long from their start, the restarts stop, and the user is
// told once, in words that say so.
func r6PastTheSizeEveryTime(t *testing.T) {
	const window = 200_000
	s, _ := r6QuietRestart(t, window, func(s *session, pass int) {
		r6SendEach(s, 1, func(int) event.UserMessageData {
			return r6WithPictures(fmt.Sprintf("@e more screenshots, set %d", pass), 7, s)
		})
	})
	r6Until(t, "the user told the restarts stop", func() bool { return r6ToldFull(s) > 0 || r6Dossiers(s) > 5 })
	if n := r6Dossiers(s); n != 3 {
		t.Fatalf("%d restarts, want 3: the quiet one and two more, each too long on its first call", n)
	}
	var notice string
	for _, m := range s.ui.said() {
		if strings.HasPrefix(m, fullNotice) {
			notice = m
		}
	}
	if !strings.Contains(notice, "three times in a row") {
		t.Fatalf("the user was told the wrong cause: %q", notice)
	}
}

// Hundreds of messages for eagent that no look took in (an outage of
// hours): the note of what was not seen stays a few lines long, gives the
// first and the last one by one, and every message between them lies in
// a span of lines it names.
func r6NoteStaysShort(t *testing.T) {
	s := r4StartSession(t, "", func(string, []map[string]any) reply { return hold() }, nil)
	var note string
	var sent []event.Event
	s.rt.sync(func() {
		for i := 0; i < 300; i++ {
			sent = append(sent, s.rt.append(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: fmt.Sprintf("@e still there? (%d)", i), Held: "the user addressed it to you"})))
		}
		note = s.rt.unseenNote(s.rt.st.LastSeq())
	})
	if !strings.Contains(note, "300 messages") {
		t.Fatalf("the note does not say how many:\n%s", note)
	}
	if n := strings.Count(note, "\n"); n > 2*unseenListed+4 {
		t.Fatalf("the note is %d lines long for 300 messages:\n%s", n, note)
	}
	place := func(ev event.Event) string { return fmt.Sprintf("%s:%d", ev.Source.File, ev.Source.Line) }
	for _, ev := range append(append([]event.Event{}, sent[:unseenListed]...), sent[len(sent)-unseenListed:]...) {
		if !strings.Contains(note, place(ev)+" (") {
			t.Fatalf("the note does not give %s one by one:\n%s", place(ev), note)
		}
	}
	spans := regexp.MustCompile(`(\d{10,}\.jsonl):(\d+)-(\d+) \((\d+) messages`).FindAllStringSubmatch(note, -1)
	covered := 0
	for _, ev := range sent[unseenListed : len(sent)-unseenListed] {
		in := false
		for _, m := range spans {
			from, _ := strconv.Atoi(m[2])
			to, _ := strconv.Atoi(m[3])
			if m[1] == ev.Source.File && ev.Source.Line >= from && ev.Source.Line <= to {
				in = true
			}
		}
		if !in {
			t.Fatalf("%s is in no span of the note:\n%s", place(ev), note)
		}
		covered++
	}
	total := 0
	for _, m := range spans {
		n, _ := strconv.Atoi(m[4])
		total += n
	}
	if total != covered {
		t.Fatalf("the spans count %d messages, want %d", total, covered)
	}
}

// What arrives around a dossier pass, and how the restarts that follow are
// bounded, on routes of every size. The sessions run side by side: each
// has its own fake provider and project, and what they share (the retry
// pause, the test key, the hooks that start an agent) is set here once.
func TestRestartsAroundADossierPass(t *testing.T) {
	r4ParallelSessions(t)
	setFor(t, &errorRetryDelays, []time.Duration{time.Millisecond})
	for _, c := range []struct {
		name string
		run  func(*testing.T)
	}{
		{"a burst during a pass gets one more restart on every route size", r6BurstOnEveryRouteSize},
		{"one excerpt during a pass fits without another restart", r6OneExcerptFits},
		{"a burst just before a pass is given by place at once", r6BurstJustBefore},
		{"pictures just before a pass are carried, then given by place", r6PicturesJustBefore},
		{"a burst during both passes stops at the bound and says what happened", r6BurstDuringBothPasses},
		{"a burst past the restart size but inside the window is restarted normally", r6BurstPastTheSizeInsideTheWindow},
		{"restarts past the size every time stop after three in a row", r6PastTheSizeEveryTime},
		{"a provider that counts more than the estimate does not restart after every look", r6ProviderCountsMore},
		{"restarts by how the provider counts", r6ByHowTheProviderCounts},
		{"the note of what was not seen stays short and loses nothing", r6NoteStaysShort},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			c.run(t)
		})
	}
}
