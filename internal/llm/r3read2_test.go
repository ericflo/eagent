package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// A picture is refused, the call goes on without it, and that retry meets
// a passing server error; the next attempt goes through. The refusal still
// reaches the caller, who writes it down: this client will not report it
// again, and a resumed session or a fallback's client knows of it only
// from the record.
func TestARefusalIsStillReportedWhenTheRetryMeetsAPassingError(t *testing.T) {
	dir := t.TempDir()
	bad := append(pngOf(t, 11, 11), []byte("trailing bytes the provider dislikes")...)
	pic := filepath.Join(dir, "b.png")
	if err := os.WriteFile(pic, bad, 0o644); err != nil {
		t.Fatal(err)
	}
	badData := base64.StdEncoding.EncodeToString(bad)
	var mu sync.Mutex
	posts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		posts++
		n := posts
		mu.Unlock()
		if strings.Contains(string(raw), badData) {
			w.WriteHeader(400)
			fmt.Fprint(w, `{"type":"error","error":{"type":"invalid_request_error","message":"Could not process image"}}`)
			return
		}
		if n == 2 {
			w.WriteHeader(503)
			fmt.Fprint(w, `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`)
			return
		}
		anthropicSSE(w, "ok")
	}))
	t.Cleanup(srv.Close)
	c := NewClient(Endpoint{Protocol: ProtocolAnthropic, BaseURL: srv.URL, Model: "claude-x", APIKey: "k"})
	c.MaxAttempts = 3
	req := Request{Messages: []Message{{Role: "user", Text: "look", Images: []Image{{Path: pic, MediaType: "image/png"}}}}}
	resp, err := c.Complete(context.Background(), req, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.RefusedImages) != 1 || resp.RefusedImages[0] != pic {
		t.Fatalf("the provider refused %s and the call went through without it, but the response reports %v refused: the refusal is never written down", filepath.Base(pic), resp.RefusedImages)
	}
}

// A picture the record says was refused (Image.Refused) on a model that
// takes no pictures at all, in a conversation that holds another picture.
// Two calls through one client with the same conversation must send the
// same request; the second must not render the refused picture's message
// differently from the first.
func TestARecordedRefusalRendersTheSameOnEveryCallToATextOnlyModel(t *testing.T) {
	dir := t.TempDir()
	x := filepath.Join(dir, "x.png")
	y := filepath.Join(dir, "y.png")
	for _, p := range []string{x, y} {
		if err := os.WriteFile(p, pngOf(t, 6, 6), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	srv, reqs := chatRefusing(t, func(req map[string]any) string {
		raw, _ := json.Marshal(req)
		if strings.Contains(string(raw), "image_url") {
			return `{"error":{"message":"This model does not support image input."}}`
		}
		return ""
	})
	c := NewClient(Endpoint{Protocol: ProtocolChat, BaseURL: srv.URL, Model: "m", APIKey: "k"})
	c.MaxAttempts = 1
	req := Request{Messages: []Message{
		{Role: "user", Text: "first", Images: []Image{{Path: x, MediaType: "image/png", Refused: true}}},
		{Role: "assistant", Text: "seen"},
		{Role: "user", Text: "second", Images: []Image{{Path: y, MediaType: "image/png"}}},
	}}
	var sent []string
	for i := 0; i < 2; i++ {
		if _, err := c.Complete(context.Background(), req, nil); err != nil {
			t.Fatal(err)
		}
		last := (*reqs)[len(*reqs)-1]
		raw, _ := json.Marshal(last["messages"])
		sent = append(sent, string(raw))
	}
	if sent[0] != sent[1] {
		t.Fatalf("the same conversation rendered two requests through one client:\nfirst:  %s\nsecond: %s", sent[0], sent[1])
	}
}

// The same when the call fails for good after the refusal: there is no
// response to carry it, so the caller asks the client, once.
func TestARefusalIsStillReportedWhenTheCallThenFails(t *testing.T) {
	dir := t.TempDir()
	bad := append(pngOf(t, 11, 11), []byte("trailing bytes the provider dislikes")...)
	pic := filepath.Join(dir, "b.png")
	if err := os.WriteFile(pic, bad, 0o644); err != nil {
		t.Fatal(err)
	}
	badData := base64.StdEncoding.EncodeToString(bad)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if strings.Contains(string(raw), badData) {
			w.WriteHeader(400)
			fmt.Fprint(w, `{"type":"error","error":{"type":"invalid_request_error","message":"Could not process image"}}`)
			return
		}
		w.WriteHeader(503)
		fmt.Fprint(w, `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`)
	}))
	t.Cleanup(srv.Close)
	c := NewClient(Endpoint{Protocol: ProtocolAnthropic, BaseURL: srv.URL, Model: "claude-x", APIKey: "k"})
	c.MaxAttempts = 1
	req := Request{Messages: []Message{{Role: "user", Text: "look", Images: []Image{{Path: pic, MediaType: "image/png"}}}}}
	if _, err := c.Complete(context.Background(), req, nil); err == nil {
		t.Fatal("the call should have failed")
	}
	if got := c.TakeRefusedImages(); len(got) != 1 || got[0] != pic {
		t.Fatalf("after the failed call the client reports %v refused, want %s", got, filepath.Base(pic))
	}
	if got := c.TakeRefusedImages(); len(got) != 0 {
		t.Fatalf("the refusal was reported twice: %v", got)
	}
}
