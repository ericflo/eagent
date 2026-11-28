package state

import (
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// A picture the record says a provider refused is marked wherever the
// conversation shows it, from the first message on, so every request made
// after the refusal carries the same note in its place.
func TestARefusedPictureIsMarkedInTheConversation(t *testing.T) {
	b := newBuilder()
	b.add(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "@e look", Attachments: []event.Attachment{
		{Name: "a.png", Path: "/s/a.png", ContentType: "image/png"},
		{Name: "b.png", Path: "/s/b.png", ContentType: "image/png"},
	}}))
	marks := func() map[string]bool {
		out := map[string]bool{}
		for _, m := range Replay(b.events).View(nil) {
			for _, im := range m.Images {
				out[im.Path] = im.Refused
			}
		}
		return out
	}
	if got := marks(); len(got) != 2 || got["/s/a.png"] || got["/s/b.png"] {
		t.Fatalf("before any refusal: %v", got)
	}
	b.add(event.New(event.ImagesRefused, event.ActorHarness, event.ImagesRefusedData{Paths: []string{"/s/b.png"}}))
	if got := marks(); len(got) != 2 || got["/s/a.png"] || !got["/s/b.png"] {
		t.Fatalf("after b.png was refused: %v", got)
	}
}
