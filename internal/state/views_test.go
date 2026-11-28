package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/prompts"
)

// User messages render as IRC-style chatlog lines with the timestamp taken
// from the persisted event time, never time.Now at render.
func TestUserMessageRendersChatlogLine(t *testing.T) {
	b := newBuilder()
	um := b.add(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "hello"}))
	web := b.add(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "from web", Source: "web"}))
	phone := b.add(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "from phone", Source: "finalechat"}))

	st := Replay(b.events)
	msgs := st.View(nil)
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d: %+v", len(msgs), msgs)
	}
	want := []struct {
		ev   event.Event
		nick string
		text string
	}{
		{um, "<user>", "hello"},
		{web, "<user@web>", "from web"},
		{phone, "<user@phone>", "from phone"},
	}
	for i, w := range want {
		ts := w.ev.Time.In(st.Location()).Format("2006-01-02 15:04:05")
		wantText := "[" + ts + "] " + w.nick + " " + w.text
		if msgs[i].Role != "user" || msgs[i].Text != wantText {
			t.Fatalf("message %d = (%q, %q), want (%q, %q)", i, msgs[i].Role, msgs[i].Text, "user", wantText)
		}
	}
}

// Replaying the same events must yield byte-identical conversations:
// no time.Now at render, so prompt-cache prefixes keep hitting.
func TestViewReplayStable(t *testing.T) {
	b := newBuilder()
	b.add(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "build it"}))
	b.add(event.New(event.Assistant, event.ActorEagent, event.AssistantData{ToolCalls: []event.ToolCall{call("c1", "relay_screen", `{}`)}, SeenSeq: 3}))
	b.add(event.New(event.ToolResult, event.ActorEagent, event.ToolResultData{CallID: "c1", Name: "relay_screen", Output: "$ ls"}))
	b.add(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "again", Source: "web"}))

	join := func(st *State) string {
		var sb strings.Builder
		for _, m := range st.View(nil) {
			sb.WriteString(m.Role + "\x00" + m.Text + "\x01")
		}
		return sb.String()
	}
	first := join(Replay(b.events))
	for i := 0; i < 3; i++ {
		if got := join(Replay(b.events)); got != first {
			t.Fatalf("replay %d differs:\n%q\nvs\n%q", i, got, first)
		}
	}
}

// A project override of USER_MESSAGE.md changes the rendering.
func TestUserMessageOverrideTemplate(t *testing.T) {
	dir := t.TempDir()
	pdir := filepath.Join(dir, ".agents", "eagent", "prompts")
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pdir, "USER_MESSAGE.md"), []byte("CUSTOM {{.Nick}} says {{.Text}}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	set, err := prompts.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	b := newBuilder()
	b.add(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "hi"}))
	st := Replay(b.events)
	st.SetPrompts(set)
	msgs := st.View(nil)
	if len(msgs) != 1 || msgs[0].Text != "CUSTOM user says hi" {
		t.Fatalf("override not applied: %+v", msgs)
	}
	// Without the override the default chatlog line renders.
	plain := Replay(b.events)
	if strings.Contains(plain.View(nil)[0].Text, "CUSTOM") {
		t.Fatalf("default view leaked the override: %q", plain.View(nil)[0].Text)
	}
}

// An answer to a question asked in an earlier subsession still carries the
// question text through the USER_ANSWER.md template: whether the briefing
// took the question in (covers 4), or it was asked after the last event
// the briefing took in and is carried over without eagent's own call
// that asked it (covers 2).
func TestUserAnswerTemplateCarriesQuestion(t *testing.T) {
	for _, covers := range []int64{4, 2} {
		st := New()
		seq := int64(0)
		add := func(ev event.Event) {
			seq++
			ev.Seq = seq
			st.Apply(ev)
		}
		add(event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{}))
		add(event.New(event.SubsessionStart, event.ActorHarness, event.SubsessionStartData{File: "1.jsonl", Index: 0, Reason: "start"}))
		add(event.New(event.Question, event.ActorEagent, event.QuestionData{ID: "q1", Text: "Which database?", Options: []string{"SQLite", "Postgres"}}))
		add(event.New(event.SubsessionEnd, event.ActorHarness, event.SubsessionEndData{Reason: "context full", NextFile: "2.jsonl"}))
		add(event.New(event.SubsessionStart, event.ActorHarness, event.SubsessionStartData{File: "2.jsonl", Index: 1, Reason: "rollover"}))
		add(event.New(event.Dossier, event.ActorHarness, event.DossierData{Text: "the dossier", CoversSeq: covers, By: "eagent"}))
		add(event.New(event.UserAnswer, event.ActorUser, event.UserAnswerData{QuestionID: "q1", Text: "SQLite"}))
		joined := ""
		for _, m := range st.View(nil) {
			joined += m.Text
		}
		if !strings.Contains(joined, "Which database?") || !strings.Contains(joined, "SQLite | Postgres") || !strings.Contains(joined, "SQLite") {
			t.Fatalf("covers %d: the answer lost its question across the rollover: %s", covers, joined)
		}
		if !strings.Contains(joined, "q1") {
			t.Fatalf("covers %d: the answer lost its question id: %s", covers, joined)
		}
	}
}
