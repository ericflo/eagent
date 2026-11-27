package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A request refused as too long for the model's context is an overflow,
// whatever else its words name: it comes back as one, no picture is left
// out or written down as refused for it, and the model is not taken for
// text-only. (Finding 9 of the thirteenth review, and a
// reader's case: a picture the provider took on the call before was written
// down as refused when the next call overflowed.) A 413 is still taken for
// the pictures', as before: they are most of a request's bytes.
func TestR13AnOverflowIsNotAPictureRefusal(t *testing.T) {
	for _, c := range []struct {
		name   string
		status int
		body   string
	}{
		{"vision model, prompt too long", 400, `{"error":{"message":"This model's maximum context length is 131072 tokens. However, you requested 140000 tokens. (model Llama-3.2-90B-Vision-Instruct)"}}`},
		{"overflow worded like a picture fault", 400, `{"error":{"code":"context_length_exceeded","message":"Your input exceeds the context window of this model, including image tokens."}}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			img := filepath.Join(t.TempDir(), "a.png")
			if err := os.WriteFile(img, []byte("\x89PNG\r\n\x1a\nfake"), 0o644); err != nil {
				t.Fatal(err)
			}
			overflow, withImage := false, 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				picture := strings.Contains(string(raw), "image_url")
				if picture {
					withImage++
				}
				// The picture is the margin: the conversation fits
				// without it, as a retry that leaves it out would find.
				if overflow && picture {
					w.WriteHeader(c.status)
					fmt.Fprint(w, c.body)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": "seen"}}}})
				fmt.Fprintf(w, "data: %s\n\n", chunk)
				fin, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}})
				fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", fin)
			}))
			defer srv.Close()
			cl := NewClient(Endpoint{Protocol: ProtocolChat, BaseURL: srv.URL, Model: "Llama-3.2-90B-Vision-Instruct", APIKey: "k"})
			cl.MaxAttempts = 1
			req := Request{Messages: []Message{{Role: "user", Text: "look", Images: []Image{{Path: img, MediaType: "image/png"}}}}}
			if _, err := cl.Complete(context.Background(), req, nil); err != nil {
				t.Fatalf("first call: %v", err)
			}
			overflow = true
			_, err := cl.Complete(context.Background(), req, nil)
			var ae *APIError
			if !errors.As(err, &ae) || ae.Status != c.status {
				t.Fatalf("the overflow came back as %v, want the provider's %d", err, c.status)
			}
			if got := cl.TakeRefusedImages(); len(got) != 0 {
				t.Fatalf("written down as refused after an overflow: %v", got)
			}
			overflow = false
			before := withImage
			if _, err := cl.Complete(context.Background(), req, nil); err != nil {
				t.Fatalf("call after the overflow: %v", err)
			}
			if withImage != before+1 {
				t.Fatalf("the picture was not sent on the call after the overflow (taken for text-only)")
			}
		})
	}
}
