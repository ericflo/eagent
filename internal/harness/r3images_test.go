package harness

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

func hasPicturePart(msgs []map[string]any) bool {
	for _, m := range msgs {
		parts, _ := m["content"].([]any)
		for _, p := range parts {
			if pm, ok := p.(map[string]any); ok && pm["type"] == "image_url" {
				return true
			}
		}
	}
	return false
}

// A picture the provider refuses, for a reason the file does not show, is
// written down. The session resumed later has a new client with no memory
// of the refusal; it renders the conversation with the same note in the
// picture's place and does not upload it to be refused again.
func TestAPictureAProviderRefusedIsWrittenDownAndStaysOutAfterAResume(t *testing.T) {
	var mu sync.Mutex
	looks, uploads := 0, 0
	var afterRefusal string
	s := startSession(t, "", func(_ string, msgs []map[string]any) reply {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case hasPicturePart(msgs):
			uploads++
			return reply{status: 400, body: `{"error":{"message":"Could not process image","type":"invalid_request_error"}}`}
		case phoneAfterLook(msgs):
			afterRefusal = allText(msgs)
			return say("I could not see that picture.")
		case strings.Contains(lastUserText(msgs), "kept for you") && looks == 0:
			looks++
			return reply{calls: []event.ToolCall{tc("view_image", `{"path":"shot.png"}`)}}
		}
		return hold()
	}, nil)
	project := s.rt.opts.Project
	if err := os.WriteFile(filepath.Join(project, "shot.png"), tinyPNG, 0o644); err != nil {
		t.Fatal(err)
	}
	s.type_("@e look at shot.png")
	s.waitFor("the answer", func() bool { return s.ui.messageCount() == 1 })
	s.settled()
	refusals := s.events(event.ImagesRefused)
	if len(refusals) != 1 {
		t.Fatalf("%d refusals written down, want 1", len(refusals))
	}
	var d event.ImagesRefusedData
	if err := refusals[0].Decode(&d); err != nil || len(d.Paths) != 1 || !strings.HasPrefix(d.Paths[0], s.rt.sess.Path) {
		t.Fatalf("refusal = %+v (%v), want the one copy kept in the session", d, err)
	}
	mu.Lock()
	if uploads != 1 {
		t.Fatalf("the picture was uploaded %d times in the first run, want once", uploads)
	}
	const note = "could not be shown to this model"
	if !strings.Contains(afterRefusal, note) {
		t.Fatalf("the call that went through carried no note for the picture:\n%s", afterRefusal)
	}
	mu.Unlock()
	path := s.rt.sess.Path
	s.stop()

	var again []string
	uploadsAfter := 0
	model := newScripted(func(_ string, msgs []map[string]any) reply {
		mu.Lock()
		defer mu.Unlock()
		if hasPicturePart(msgs) {
			uploadsAfter++
			return reply{status: 400, body: `{"error":{"message":"Could not process image","type":"invalid_request_error"}}`}
		}
		again = append(again, allText(msgs))
		if len(again) == 1 {
			return say("Still nothing to see.")
		}
		return hold()
	})
	t.Cleanup(model.srv.Close)
	ui := &fakeUI{input: make(chan string, 4)}
	rt, err := Resume(testConfig(model.srv.URL), Options{Project: project, Interactive: true, Prompt: "@e and now?"}, ui, path)
	if err != nil {
		t.Fatal(err)
	}
	r := &session{t: t, rt: rt, ui: ui, seat: s.seat, model: model, done: make(chan int, 1)}
	ctx, cancel := contextWithCancel()
	r.cancel = cancel
	go func() { r.done <- rt.Run(ctx) }()
	t.Cleanup(r.stop)
	r.waitFor("the answer after the resume", func() bool { return ui.messageCount() == 1 })
	r.settled()
	mu.Lock()
	defer mu.Unlock()
	if uploadsAfter != 0 {
		t.Fatalf("the resumed session uploaded the refused picture %d times", uploadsAfter)
	}
	if len(again) == 0 || !strings.Contains(again[0], note) {
		t.Fatalf("the resumed session's request has no note for the picture: %q", again)
	}
	if got := len(r.events(event.ImagesRefused)); got != 1 {
		t.Fatalf("%d refusals in the record after the resume, want the one", got)
	}
}
