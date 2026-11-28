package llm

import "testing"

// Marking pictures as refused changes only those pictures, and leaves the
// request it was made from as it was: the caller may still hold it.
func TestWithRefusedMarksOnlyThePicturesNamedAndCopies(t *testing.T) {
	req := Request{Messages: []Message{
		{Role: "user", Text: "one", Images: []Image{{Path: "/s/a.png"}, {Path: "/s/b.png"}}},
		{Role: "assistant", Text: "seen"},
		{Role: "user", Text: "two", Images: []Image{{Path: "/s/b.png"}}},
	}}
	got := req.WithRefused([]string{"/s/b.png"})
	if req.Messages[0].Images[1].Refused || req.Messages[2].Images[0].Refused {
		t.Fatal("the original request was changed")
	}
	if got.Messages[0].Images[0].Refused || !got.Messages[0].Images[1].Refused || !got.Messages[2].Images[0].Refused {
		t.Fatalf("marks = %+v", got.Messages)
	}
	if same := req.WithRefused(nil); len(same.Messages) != 3 || &same.Messages[0] != &req.Messages[0] {
		t.Fatal("nothing to mark should return the request as it is")
	}
}
