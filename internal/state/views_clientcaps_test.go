package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/clientcaps"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/prompts"
)

// readTestTime is the fixed clock for hand-built states, mirroring newBuilder.
func readTestTime(t *testing.T) time.Time {
	t.Helper()
	return time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
}

// A user.message carrying client caps renders them as a trailing client line.
func TestUserMessageWithCapsRendersClientLine(t *testing.T) {
	b := newBuilder()
	ev := b.add(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{
		Text:   "hi",
		Source: "web",
		Client: &clientcaps.Caps{Source: "web", Timezone: "America/Los_Angeles", Locale: "en-US", Supplies: []string{"tz", "locale", "screen"}},
	}))

	st := Replay(b.events)
	msgs := st.OrchestratorView()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d: %+v", len(msgs), msgs)
	}
	ts := ev.Time.Local().Format("2006-01-02 15:04:05")
	want := "[" + ts + "] <user@web> hi [web · tz America/Los_Angeles · en-US]\n"
	if msgs[0].Text != want {
		t.Fatalf("got %q, want %q", msgs[0].Text, want)
	}
}

// Without caps the rendering must be byte-identical to the old format.
func TestUserMessageWithoutCapsIsByteIdentical(t *testing.T) {
	b := newBuilder()
	um := b.add(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "hello"}))
	web := b.add(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "from web", Source: "web"}))

	st := Replay(b.events)
	msgs := st.OrchestratorView()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	for i, w := range []struct {
		ev   event.Event
		nick string
		text string
	}{{um, "<user>", "hello"}, {web, "<user@web>", "from web"}} {
		ts := w.ev.Time.Local().Format("2006-01-02 15:04:05")
		want := "[" + ts + "] " + w.nick + " " + w.text + "\n"
		if msgs[i].Text != want {
			t.Fatalf("message %d = %q, want %q", i, msgs[i].Text, want)
		}
	}
}

// A user.answer with caps carries the client line after the question note.
func TestUserAnswerWithCapsRendersClientLine(t *testing.T) {
	st := New()
	var seq int64
	base := readTestTime(t)
	add := func(ev event.Event) event.Event {
		seq++
		ev.Seq = seq
		base = base.Add(time.Second)
		ev.Time = base
		st.Apply(ev)
		return ev
	}
	add(event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{}))
	add(event.New(event.SubsessionStart, event.ActorHarness, event.SubsessionStartData{File: "1.jsonl", Index: 0, Reason: "start"}))
	add(event.New(event.NarratorQuestion, event.ActorNarrator, event.NarratorQuestionData{ID: "q1", Text: "Pick one", Options: []string{"a", "b"}}))
	atev := add(event.New(event.UserAnswer, event.ActorUser, event.UserAnswerData{
		QuestionID: "q1",
		Text:       "a",
		Source:     "finalechat",
		Client:     &clientcaps.Caps{Source: "finalechat", Timezone: "America/Los_Angeles", Locale: "en-US", Device: "phone"},
	}))
	msgs := st.OrchestratorView()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d: %+v", len(msgs), msgs)
	}
	ts := atev.Time.Local().Format("2006-01-02 15:04:05")
	want := "[" + ts + "] <user@phone> a\n[The user answered question q1] [finalechat · tz America/Los_Angeles · en-US · phone]\n"
	if msgs[1].Text != want {
		t.Fatalf("got %q, want %q", msgs[1].Text, want)
	}
}

// A project override can use .ClientLine (and .Client); without caps it is "".
func TestUserMessageOverrideCanUseClientLine(t *testing.T) {
	dir := t.TempDir()
	pdir := filepath.Join(dir, ".agents", "eagent", "prompts")
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pdir, "USER_MESSAGE.md"), []byte("{{.Nick}}: {{.Text}}|{{.Client}}|{{.ClientLine}}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	set, err := prompts.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	b := newBuilder()
	b.add(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{
		Text: "hi", Source: "web",
		Client: &clientcaps.Caps{Source: "web", Timezone: "America/Los_Angeles", Locale: "en-US"},
	}))
	b.add(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "plain"}))
	st := Replay(b.events)
	st.SetPrompts(set)
	msgs := st.OrchestratorView()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Text != "user@web: hi|web|web · tz America/Los_Angeles · en-US\n" {
		t.Fatalf("caps override = %q", msgs[0].Text)
	}
	if !strings.HasSuffix(msgs[1].Text, "plain||\n") {
		t.Fatalf("no-caps override = %q, want empty Client/ClientLine", msgs[1].Text)
	}
}
