package llm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// imageServer is a fake Anthropic server that refuses any request whose
// body contains one of the refused strings, with the given status and body.
type imageServer struct {
	mu      sync.Mutex
	bodies  []string
	refuse  []string
	status  int
	message string
	srv     *httptest.Server
}

func newImageServer(t *testing.T, status int, message string, refuse ...string) *imageServer {
	s := &imageServer{refuse: refuse, status: status, message: message}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.bodies = append(s.bodies, string(raw))
		s.mu.Unlock()
		for _, x := range s.refuse {
			if strings.Contains(string(raw), x) {
				w.WriteHeader(s.status)
				fmt.Fprint(w, s.message)
				return
			}
		}
		anthropicSSE(w, "ok")
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func writeFile(t *testing.T, path string, data []byte) string {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func pngOf(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// A picture Anthropic will certainly refuse for its size (over 10 MB once
// encoded, or over 8000 pixels a side) is never uploaded. It is replaced
// by a note, decided from the file alone, so every call renders the same
// request, a new client (after a resume) too.
func TestAPictureOverAnthropicsLimitIsNeverUploaded(t *testing.T) {
	dir := t.TempDir()
	big := writeFile(t, filepath.Join(dir, "big.png"), append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{7}, 8<<20)...))
	wide := writeFile(t, filepath.Join(dir, "wide.png"), pngOf(t, 8001, 4))
	fine := writeFile(t, filepath.Join(dir, "fine.png"), pngOf(t, 40, 30))
	srv := newImageServer(t, 400, `{"type":"error","error":{"type":"invalid_request_error","message":"image exceeds 10 MB maximum"}}`, "never refused")
	req := Request{Messages: []Message{
		{Role: "user", Text: "[10:00] <user> here are three", Images: []Image{{Path: big, MediaType: "image/png"}, {Path: wide, MediaType: "image/png"}, {Path: fine, MediaType: "image/png"}}},
	}}
	for i := 0; i < 2; i++ {
		c := NewClient(Endpoint{Protocol: ProtocolAnthropic, BaseURL: srv.srv.URL, Model: "claude", APIKey: "k"})
		c.MaxAttempts = 1
		if _, err := c.Complete(context.Background(), req, nil); err != nil {
			t.Fatal(err)
		}
	}
	if len(srv.bodies) != 2 || srv.bodies[0] != srv.bodies[1] {
		t.Fatalf("%d posts; the same record rendered differently", len(srv.bodies))
	}
	body := srv.bodies[0]
	if n := strings.Count(body, `"type":"image"`); n != 1 || !strings.Contains(body, base64.StdEncoding.EncodeToString(mustRead(t, fine))) {
		t.Fatalf("%d pictures uploaded, want only the fine one", n)
	}
	if len(body) > 1<<20 {
		t.Fatalf("the big picture went up: the body is %d bytes", len(body))
	}
	for _, name := range []string{"big.png", "wide.png"} {
		if !strings.Contains(body, name) {
			t.Fatalf("no note about %s in %s", name, body)
		}
	}
	// The other protocols keep their own, larger, limit.
	if got := fitImages(req, ProtocolChat); len(got.Messages[0].Images) != 3 {
		t.Fatalf("chat: %d pictures kept", len(got.Messages[0].Images))
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// A picture the provider refused for a reason eagent cannot tell in
// advance is not uploaded again on every later call, and the pictures the
// provider had already taken stay in the conversation, so the request
// still extends the one before.
func TestARefusedPictureIsNotUploadedOnEveryCall(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		message string
	}{
		{"could not process", 400, `{"type":"error","error":{"type":"invalid_request_error","message":"messages.1.content.1.image.source.base64: could not process image"}}`},
		{"request too large", 413, `{"type":"error","error":{"type":"request_too_large","message":"Request exceeds the maximum allowed number of bytes."}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			a := writeFile(t, filepath.Join(dir, "a.png"), pngOf(t, 10, 10))
			b := writeFile(t, filepath.Join(dir, "b.png"), pngOf(t, 20, 20))
			bData := base64.StdEncoding.EncodeToString(mustRead(t, b))
			aData := base64.StdEncoding.EncodeToString(mustRead(t, a))
			srv := newImageServer(t, tc.status, tc.message, bData)
			c := NewClient(Endpoint{Protocol: ProtocolAnthropic, BaseURL: srv.srv.URL, Model: "claude", APIKey: "k"})
			c.MaxAttempts = 1
			var notes []string
			c.OnRetry = func(_ int, err error, _ time.Duration) { notes = append(notes, err.Error()) }
			msgs := []Message{{Role: "user", Text: "first", Images: []Image{{Path: a, MediaType: "image/png"}}}}
			call := func(add ...Message) {
				t.Helper()
				msgs = append(msgs, add...)
				if _, err := c.Complete(context.Background(), Request{Messages: msgs}, nil); err != nil {
					t.Fatal(err)
				}
			}
			call()
			call(Message{Role: "assistant", Text: "seen"}, Message{Role: "user", Text: "second", Images: []Image{{Path: b, MediaType: "image/png"}}})
			if len(srv.bodies) != 3 {
				t.Fatalf("%d posts after the refusal, want 3", len(srv.bodies))
			}
			retry := srv.bodies[2]
			if strings.Contains(retry, bData) || !strings.Contains(retry, aData) || !strings.Contains(retry, "b.png") {
				t.Fatalf("the retry should keep a.png and leave out b.png with a note: %.400s", retry)
			}
			call(Message{Role: "assistant", Text: "ok"}, Message{Role: "user", Text: "third"})
			if len(srv.bodies) != 4 {
				t.Fatalf("%d posts: the refused picture was uploaded again", len(srv.bodies))
			}
			if a, b := messagesOf(t, retry), messagesOf(t, srv.bodies[3]); len(b) < len(a) || strings.Join(b[:len(a)], "\n") != strings.Join(a, "\n") {
				t.Fatalf("the next request does not extend the retry:\n%v\n%v", a, b)
			}
			if len(notes) != 1 {
				t.Fatalf("notes = %q", notes)
			}
		})
	}
}

// messagesOf returns the messages of an Anthropic request, each as JSON
// without its breakpoints.
func messagesOf(t *testing.T, body string) []string {
	t.Helper()
	var req struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, m := range req.Messages {
		out = append(out, strings.ReplaceAll(string(m), `"cache_control":{"type":"ephemeral"},`, ""))
	}
	return out
}
