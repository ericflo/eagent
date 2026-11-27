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

// A 413 over a body limit that the conversation's text alone is past is
// no refusal of its pictures: leaving them out does not get past it. At
// 225c781 (1a4230a) the client wrote none down; on main a 413 was never a
// picture refusal. Here the picture the provider took on the call before
// is written down as refused too.
func TestRV13A413ForTheTextAloneRefusesNoPicture(t *testing.T) {
	for _, tc := range []struct {
		name     string
		protocol string
		ok       func(http.ResponseWriter)
		body     string
	}{
		{"gateway body limit (nginx)", ProtocolChat, func(w http.ResponseWriter) {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		}, "<html><head><title>413 Request Entity Too Large</title></head><body><center><h1>413 Request Entity Too Large</h1></center><hr><center>nginx</center></body></html>"},
		{"anthropic request_too_large", ProtocolAnthropic, func(w http.ResponseWriter) { anthropicSSE(w, "ok") },
			`{"type":"error","error":{"type":"request_too_large","message":"Request exceeds the maximum allowed number of bytes."}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			a := writeFile(t, filepath.Join(dir, "a.png"), pngOf(t, 10, 10))
			b := writeFile(t, filepath.Join(dir, "b.png"), pngOf(t, 20, 20))
			aData := base64.StdEncoding.EncodeToString(mustRead(t, a))
			bData := base64.StdEncoding.EncodeToString(mustRead(t, b))
			const limit = 3000
			var mu sync.Mutex
			var bodies []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				mu.Lock()
				bodies = append(bodies, string(raw))
				mu.Unlock()
				if len(raw) > limit {
					w.WriteHeader(413)
					fmt.Fprint(w, tc.body)
					return
				}
				tc.ok(w)
			}))
			defer srv.Close()
			c := NewClient(Endpoint{Protocol: tc.protocol, BaseURL: srv.URL, Model: "m", APIKey: "k"})
			c.MaxAttempts = 1
			first := []Message{{Role: "user", Text: "first", Images: []Image{{Path: a, MediaType: "image/png"}}}}
			if _, err := c.Complete(context.Background(), Request{Messages: first}, nil); err != nil {
				t.Fatal(err)
			}
			long := append(append([]Message(nil), first...), Message{Role: "assistant", Text: "seen"},
				Message{Role: "user", Text: "second, with a long log " + strings.Repeat("x", 2*limit), Images: []Image{{Path: b, MediaType: "image/png"}}})
			_, err := c.Complete(context.Background(), Request{Messages: long}, nil)
			var ae *APIError
			if !errors.As(err, &ae) || ae.Status != 413 {
				t.Fatalf("err = %v, want the 413", err)
			}
			if got := c.TakeRefusedImages(); len(got) != 0 {
				t.Errorf("the text alone was over the limit, yet %v are reported refused (the record writes them down for good; a was taken on the call before)", got)
			}
			// The conversation made shorter (a restart): both pictures fit.
			short := append(append([]Message(nil), first...), Message{Role: "assistant", Text: "seen"},
				Message{Role: "user", Text: "second", Images: []Image{{Path: b, MediaType: "image/png"}}})
			mu.Lock()
			before := len(bodies)
			mu.Unlock()
			if _, err := c.Complete(context.Background(), Request{Messages: short}, nil); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			next := bodies[before]
			mu.Unlock()
			if !strings.Contains(next, aData) || !strings.Contains(next, bData) {
				t.Errorf("the shorter conversation left a picture out the provider takes: a sent=%v b sent=%v", strings.Contains(next, aData), strings.Contains(next, bData))
			}
		})
	}
}
