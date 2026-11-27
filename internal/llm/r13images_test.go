package llm

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// scripted answers the nth post (from 1) with answer, and keeps every body
// it was sent.
func scripted(t *testing.T, answer func(n int, body string, w http.ResponseWriter, r *http.Request)) (string, func() []string) {
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(raw))
		n := len(bodies)
		mu.Unlock()
		answer(n, string(raw), w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), bodies...)
	}
}

// responsesOK writes one complete streamed Responses reply.
func responsesOK(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprint(w, "data: "+`{"type":"response.completed","response":{"status":"completed","output":[{"type":"message","id":"m1","status":"completed","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":5,"output_tokens":1}}}`+"\n\n")
}

// An overflow is not the model refusing pictures, even when its wording
// names a vision model or the image tokens: when the request stays too
// long without its pictures, the client is not taken for text-only and
// the next call still sends them.
func TestAnOverflowThatNamesPicturesDoesNotMakeTheModelTextOnly(t *testing.T) {
	for _, tc := range []struct {
		name     string
		protocol string
		ok       func(http.ResponseWriter)
		overflow func(http.ResponseWriter)
	}{
		{"chat 400 naming a vision model", ProtocolChat, chatOK, func(w http.ResponseWriter) {
			w.WriteHeader(400)
			fmt.Fprint(w, `{"error":{"message":"Llama-3.2-90B-Vision-Instruct: prompt is too long: 135000 tokens > 128000 maximum","type":"invalid_request_error"}}`)
		}},
		{"responses stream context_length_exceeded counting image tokens", ProtocolResponses, responsesOK, func(w http.ResponseWriter) {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: "+`{"type":"response.failed","response":{"status":"failed","error":{"code":"context_length_exceeded","message":"This model's maximum context length is 128000 tokens. However, your messages resulted in 135000 tokens (including image tokens)."}}}`+"\n\n")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			a := writeFile(t, filepath.Join(dir, "a.png"), pngOf(t, 10, 10))
			b := writeFile(t, filepath.Join(dir, "b.png"), pngOf(t, 20, 20))
			aData := base64.StdEncoding.EncodeToString(mustRead(t, a))
			bData := base64.StdEncoding.EncodeToString(mustRead(t, b))
			url, bodies := scripted(t, func(n int, _ string, w http.ResponseWriter, _ *http.Request) {
				if n == 1 {
					tc.ok(w)
					return
				}
				tc.overflow(w)
			})
			c := NewClient(Endpoint{Protocol: tc.protocol, BaseURL: url, Model: "m", APIKey: "k"})
			c.MaxAttempts = 1
			msgs := []Message{{Role: "user", Text: "first", Images: []Image{{Path: a, MediaType: "image/png"}}}}
			if _, err := c.Complete(context.Background(), Request{Messages: msgs}, nil); err != nil {
				t.Fatal(err)
			}
			msgs = append(msgs, Message{Role: "assistant", Text: "seen"},
				Message{Role: "user", Text: "second, with a long log " + strings.Repeat("x", 4000), Images: []Image{{Path: b, MediaType: "image/png"}}})
			_, err := c.Complete(context.Background(), Request{Messages: msgs}, nil)
			var ae *APIError
			if !errors.As(err, &ae) || !ae.ContextOverflow() {
				t.Fatalf("err = %v, want the provider's overflow", err)
			}
			if got := c.TakeRefusedImages(); len(got) != 0 {
				t.Fatalf("the request stayed too long without its pictures, yet %v are reported refused", got)
			}
			before := len(bodies())
			_, _ = c.Complete(context.Background(), Request{Messages: msgs}, nil)
			next := bodies()[before]
			if !strings.Contains(next, aData) || !strings.Contains(next, bData) {
				t.Fatalf("an overflow made the client text-only: the next call left a picture out (a=%v b=%v)", strings.Contains(next, aData), strings.Contains(next, bData))
			}
		})
	}
}
