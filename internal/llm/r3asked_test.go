package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A provider can name the model that ran in a way that does not look like
// the one that was asked for. The response carries both, and the name to
// hold its native items to is the one that was asked for.
func TestAResponseSaysWhichModelWasAsked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, e := range []string{
			`{"type":"response.output_item.done","item":{"type":"message","id":"m1","status":"completed","role":"assistant","content":[{"type":"output_text","text":"Hi"}]}}`,
			`{"type":"response.completed","response":{"model":"sol-snapshot-b","status":"completed","output":[{"type":"message","id":"m1","status":"completed","role":"assistant","content":[{"type":"output_text","text":"Hi"}]}],"usage":{"input_tokens":5,"output_tokens":1}}}`,
		} {
			fmt.Fprintf(w, "data: %s\n\n", e)
		}
	}))
	defer srv.Close()
	c := NewClient(Endpoint{Protocol: ProtocolResponses, BaseURL: srv.URL, Model: "gpt-6.1-sol", APIKey: "k"})
	resp, err := c.Complete(context.Background(), Request{System: "sys", Messages: []Message{{Role: "user", Text: "hi"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Model != "sol-snapshot-b" || resp.Asked != "gpt-6.1-sol" || resp.By() != "gpt-6.1-sol" {
		t.Fatalf("model %q, asked %q, by %q", resp.Model, resp.Asked, resp.By())
	}
	if by := (&Response{Model: "m"}).By(); by != "m" {
		t.Fatalf("a response that does not say what was asked is by %q", by)
	}
}

// A turn recorded with the model that was asked for goes back to that
// model with its reasoning, whatever the provider called the model that
// ran; and not to another model.
func TestATurnHeldToTheModelAskedReplaysToIt(t *testing.T) {
	turn := func(host string) []Message { return responsesTurn(host, "gpt-6.1-sol") }
	if items := responsesWire(t, "gpt-6.1-sol", turn); !strings.Contains(toJSON(items), "gAAAA-astra") {
		t.Fatal("the reasoning did not go back to the model that was asked")
	}
	if items := responsesWire(t, "gpt-6.1-sol-mini", turn); strings.Contains(toJSON(items), "gAAAA-astra") {
		t.Fatal("the reasoning went to another model")
	}
}
