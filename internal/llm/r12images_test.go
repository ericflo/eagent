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

// grownTooLarge answers the first post with ok and refuses every later one
// with status and body, as a provider does once the conversation's text
// has grown past its limit. It keeps every body it was sent.
func grownTooLarge(t *testing.T, ok func(http.ResponseWriter), status int, body string) (*httptest.Server, func() []string) {
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(raw))
		n := len(bodies)
		mu.Unlock()
		if n == 1 {
			ok(w)
			return
		}
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), bodies...)
	}
}

func chatOK(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
}

// A request too large for its text alone is not a refusal of its
// pictures: leaving them out does not get past it, so none is written down
// as refused (the caller would record images.refused from what the client
// reports), and the next call still sends them.
func TestATooLargeRequestThatStaysTooLargeWithoutItsPicturesRefusesNone(t *testing.T) {
	for _, tc := range []struct {
		name     string
		protocol string
		ok       func(http.ResponseWriter)
		status   int
		body     string
	}{
		{"413 request_too_large", ProtocolAnthropic, func(w http.ResponseWriter) { anthropicSSE(w, "ok") }, 413,
			`{"type":"error","error":{"type":"request_too_large","message":"Request exceeds the maximum allowed number of bytes."}}`},
		{"400 overflow naming a vision model", ProtocolChat, chatOK, 400,
			`{"error":{"message":"Llama-3.2-90B-Vision-Instruct: prompt exceeds the maximum context length","type":"invalid_request_error"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			a := writeFile(t, filepath.Join(dir, "a.png"), pngOf(t, 10, 10))
			b := writeFile(t, filepath.Join(dir, "b.png"), pngOf(t, 20, 20))
			aData := base64.StdEncoding.EncodeToString(mustRead(t, a))
			bData := base64.StdEncoding.EncodeToString(mustRead(t, b))
			srv, bodies := grownTooLarge(t, tc.ok, tc.status, tc.body)
			c := NewClient(Endpoint{Protocol: tc.protocol, BaseURL: srv.URL, Model: "m", APIKey: "k"})
			c.MaxAttempts = 1
			msgs := []Message{{Role: "user", Text: "first", Images: []Image{{Path: a, MediaType: "image/png"}}}}
			if _, err := c.Complete(context.Background(), Request{Messages: msgs}, nil); err != nil {
				t.Fatal(err)
			}
			msgs = append(msgs, Message{Role: "assistant", Text: "seen"},
				Message{Role: "user", Text: "second, with a long log " + strings.Repeat("x", 4000), Images: []Image{{Path: b, MediaType: "image/png"}}})
			_, err := c.Complete(context.Background(), Request{Messages: msgs}, nil)
			var ae *APIError
			if !errors.As(err, &ae) || ae.Status != tc.status || (tc.status != 413 && !ae.ContextOverflow()) {
				t.Fatalf("err = %v, want the provider's overflow", err)
			}
			if got := c.TakeRefusedImages(); len(got) != 0 {
				t.Fatalf("the request stayed too large without its pictures, yet the client reports %v refused: the record would hide them for good", got)
			}
			before := len(bodies())
			_, _ = c.Complete(context.Background(), Request{Messages: msgs}, nil)
			next := bodies()[before]
			if !strings.Contains(next, aData) || !strings.Contains(next, bData) {
				t.Fatalf("the next call left a picture out: a=%v b=%v", strings.Contains(next, aData), strings.Contains(next, bData))
			}
		})
	}
}

// A request too large with its pictures that goes through without them
// had its pictures refused: they are reported, the ones of both passes
// when it took leaving them all out. (One that then fails otherwise
// reports none: r13images_test.go.)
func TestATooLargeRequestThatGetsPastWithoutItsPicturesRefusesThem(t *testing.T) {
	const tooLarge = `{"type":"error","error":{"type":"request_too_large","message":"Request exceeds the maximum allowed number of bytes."}}`
	for _, tc := range []struct {
		name string
		then int // the status once a is left out; 0 goes through
	}{
		{"goes through", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			a := writeFile(t, filepath.Join(dir, "a.png"), pngOf(t, 10, 10))
			b := writeFile(t, filepath.Join(dir, "b.png"), pngOf(t, 20, 20))
			aData := base64.StdEncoding.EncodeToString(mustRead(t, a))
			var mu sync.Mutex
			posts := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				mu.Lock()
				posts++
				n := posts
				mu.Unlock()
				switch {
				case n > 1 && strings.Contains(string(raw), aData):
					// a and the conversation since are over the limit.
					w.WriteHeader(413)
					fmt.Fprint(w, tooLarge)
				case n > 1 && tc.then != 0:
					w.WriteHeader(tc.then)
					fmt.Fprint(w, `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`)
				default:
					anthropicSSE(w, "ok")
				}
			}))
			t.Cleanup(srv.Close)
			c := NewClient(Endpoint{Protocol: ProtocolAnthropic, BaseURL: srv.URL, Model: "claude", APIKey: "k"})
			c.MaxAttempts = 1
			msgs := []Message{{Role: "user", Text: "first", Images: []Image{{Path: a, MediaType: "image/png"}}}}
			if _, err := c.Complete(context.Background(), Request{Messages: msgs}, nil); err != nil {
				t.Fatal(err)
			}
			msgs = append(msgs, Message{Role: "assistant", Text: "seen"}, Message{Role: "user", Text: "second", Images: []Image{{Path: b, MediaType: "image/png"}}})
			resp, err := c.Complete(context.Background(), Request{Messages: msgs}, nil)
			var got []string
			if tc.then == 0 {
				if err != nil {
					t.Fatal(err)
				}
				got = resp.RefusedImages
			} else {
				if err == nil {
					t.Fatal("the call should have failed")
				}
				got = c.TakeRefusedImages()
			}
			// b is left out first; the request is still too large with a,
			// so a is left out too, and that gets past the limit.
			if want := []string{b, a}; strings.Join(got, ",") != strings.Join(want, ",") {
				t.Fatalf("reported %v refused, want %v", got, want)
			}
			mu.Lock()
			defer mu.Unlock()
			if posts != 4 {
				t.Fatalf("%d posts, want 4", posts)
			}
		})
	}
}
