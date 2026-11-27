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
	"time"
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

// A picture left out over a request too large is written down as refused
// only once a call goes through without it. A retry that is canceled,
// times out, loses its connection or meets a passing failure does not show
// that leaving it out got past the limit (the next attempt may be refused
// as too large on its text alone): no picture is reported refused, and the
// next call sends it again.
func TestAPictureLeftOutOverASizeLimitIsNotRefusedWhenTheRetryProvesNothing(t *testing.T) {
	const tooLarge = `{"type":"error","error":{"type":"request_too_large","message":"Request exceeds the maximum allowed number of bytes."}}`
	for _, tc := range []struct {
		name  string
		retry func(w http.ResponseWriter, r *http.Request)
	}{
		{"canceled", func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }},
		{"timed out", func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }},
		{"connection lost", func(w http.ResponseWriter, r *http.Request) {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				conn.Close()
			}
		}},
		{"overloaded", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(529)
			fmt.Fprint(w, `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shot := writeFile(t, filepath.Join(t.TempDir(), "shot.png"), pngOf(t, 10, 10))
			shotData := base64.StdEncoding.EncodeToString(mustRead(t, shot))
			retrying := make(chan struct{}, 1)
			url, bodies := scripted(t, func(n int, _ string, w http.ResponseWriter, r *http.Request) {
				switch n {
				case 1:
					w.WriteHeader(413)
					fmt.Fprint(w, tooLarge)
				case 2:
					retrying <- struct{}{}
					tc.retry(w, r)
				default:
					anthropicSSE(w, "ok")
				}
			})
			c := NewClient(Endpoint{Protocol: ProtocolAnthropic, BaseURL: url, Model: "claude", APIKey: "k"})
			c.MaxAttempts = 1
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch tc.name {
			case "canceled":
				go func() { <-retrying; cancel() }()
			case "timed out":
				c.CallTimeout = 100 * time.Millisecond
			}
			req := Request{Messages: []Message{{Role: "user", Text: "look", Images: []Image{{Path: shot, MediaType: "image/png"}}}}}
			if _, err := c.Complete(ctx, req, nil); err == nil {
				t.Fatal("the call should have failed")
			}
			if got := c.TakeRefusedImages(); len(got) != 0 {
				t.Fatalf("the retry without the picture proved nothing, yet %v is reported refused: the record would hide it for good", got)
			}
			c.CallTimeout = time.Minute
			if _, err := c.Complete(context.Background(), req, nil); err != nil {
				t.Fatal(err)
			}
			if got := bodies(); len(got) != 3 || !strings.Contains(got[2], shotData) {
				t.Fatalf("%d posts; the next call left the picture out", len(got))
			}
		})
	}
}

// A model found to take no pictures on the retry after one was left out
// (a request too large, or a picture refused for itself, then "does not
// support image input") is taken for text-only. No picture is written down
// as refused for that: the record would hide it from every later route, a
// vision model too. The request that went through renders as the next one
// does.
func TestAModelFoundTextOnlyOnTheRetryHasNoPictureRefusedForIt(t *testing.T) {
	const textOnly = `{"error":{"message":"This model does not support image input.","type":"invalid_request_error"}}`
	for _, tc := range []struct {
		name    string
		status  int
		body    string
		refused bool // the first refusal names b itself
	}{
		{"413, then text-only", 413, `{"error":{"message":"Request body exceeds the 8 MiB limit.","type":"invalid_request_error"}}`, false},
		{"bad picture, then text-only", 400, `{"error":{"message":"Invalid image.","type":"invalid_request_error","code":"invalid_image"}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			a := writeFile(t, filepath.Join(dir, "a.png"), pngOf(t, 10, 10))
			b := writeFile(t, filepath.Join(dir, "b.png"), pngOf(t, 20, 20))
			url, bodies := scripted(t, func(n int, _ string, w http.ResponseWriter, _ *http.Request) {
				switch n {
				case 1:
					w.WriteHeader(tc.status)
					fmt.Fprint(w, tc.body)
				case 2:
					w.WriteHeader(400)
					fmt.Fprint(w, textOnly)
				default:
					chatOK(w)
				}
			})
			c := NewClient(Endpoint{Protocol: ProtocolChat, BaseURL: url, Model: "m", APIKey: "k"})
			c.MaxAttempts = 1
			// a came before the last reply (another route took it); b is new.
			req := Request{Messages: []Message{
				{Role: "user", Text: "first", Images: []Image{{Path: a, MediaType: "image/png"}}},
				{Role: "assistant", Text: "seen"},
				{Role: "user", Text: "second", Images: []Image{{Path: b, MediaType: "image/png"}}},
			}}
			resp, err := c.Complete(context.Background(), req, nil)
			if err != nil {
				t.Fatal(err)
			}
			var want []string
			if tc.refused {
				want = []string{b}
			}
			if got := append(resp.RefusedImages, c.TakeRefusedImages()...); strings.Join(got, ",") != strings.Join(want, ",") {
				t.Fatalf("reported %v refused, want %v: a text-only model's refusal was written down as the pictures'", got, want)
			}
			if !c.rejectsImages.Load() {
				t.Fatal("the model said it takes no pictures, yet the client was not taken for text-only")
			}
			if _, err := c.Complete(context.Background(), req, nil); err != nil {
				t.Fatal(err)
			}
			got := bodies()
			if len(got) != 4 || got[2] != got[3] {
				t.Fatalf("%d posts; the request that went through rendered otherwise than the next one:\n%s\n%s", len(got), got[2], got[len(got)-1])
			}
		})
	}
}
