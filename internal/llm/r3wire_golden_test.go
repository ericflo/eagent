package llm

import (
	"bytes"
	"context"
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

	"github.com/ericflo/eagent/internal/event"
)

// wireCapture is a fake provider that keeps the body of every request and
// answers each with a short plain reply.
type wireCapture struct {
	srv    *httptest.Server
	mu     sync.Mutex
	bodies [][]byte
}

func newWireCapture(t *testing.T, protocol string) *wireCapture {
	t.Helper()
	reply := map[string]string{
		ProtocolAnthropic: `{"type":"message","model":"m","stop_reason":"end_turn","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`,
		ProtocolResponses: `{"model":"m","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}`,
		ProtocolChat:      `{"model":"m","choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`,
	}[protocol]
	w := &wireCapture{}
	w.srv = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		w.mu.Lock()
		w.bodies = append(w.bodies, raw)
		w.mu.Unlock()
		rw.Header().Set("Content-Type", "application/json")
		fmt.Fprint(rw, reply)
	}))
	t.Cleanup(w.srv.Close)
	return w
}

// send makes one call with the given conversation and returns the body
// that went on the wire.
func (w *wireCapture) send(t *testing.T, ep Endpoint, msgs []Message) []byte {
	t.Helper()
	ep.BaseURL = w.srv.URL
	c := NewClient(ep)
	c.MaxAttempts = 1
	req := Request{
		System:   "You are eagent.",
		Messages: msgs,
		Tools: []Tool{
			{Name: "relay_screen", Description: "Look at the agent's screen.", Parameters: json.RawMessage(`{"type":"object","properties":{}}`)},
			{Name: "send_message", Description: "Tell the user.", Parameters: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}}}`)},
		},
		CacheKey: "session-1",
	}
	if _, err := c.Complete(context.Background(), req, nil); err != nil {
		t.Fatal(err)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.bodies[len(w.bodies)-1]
}

// ordinaryWireSession is a conversation as eagent's record renders it on one
// route: the route's own turns carry their native items and are replayed at
// the host that made them; one earlier turn came from another provider.
// host is filled in with the fake server's address.
func ordinaryWireSession(protocol, host string) []Message {
	screen := event.ToolCall{ID: "call_q7Hk2", Name: "relay_screen", Args: json.RawMessage(`{}`)}
	// A turn that came from another provider, rendered from its text and
	// calls. Its ids are ones every protocol takes.
	foreign := Message{Role: "assistant", Text: "Looking at the screen.", ToolCalls: []event.ToolCall{screen},
		Native: json.RawMessage(`{"reasoning_content":"the user wants a status"}`), NativeProtocol: ProtocolChat, NativeHost: "https://elsewhere.example/v1", NativeModel: "kimi-k3"}
	foreignResult := Message{Role: "tool", Results: []ToolResult{{CallID: "call_q7Hk2", Name: "relay_screen", Output: "codex> all tests pass"}}}

	var own1, own2 Message
	switch protocol {
	case ProtocolAnthropic:
		own1 = Message{Role: "assistant", Text: "Checking again.", ToolCalls: []event.ToolCall{{ID: "toolu_01AbC", Name: "relay_screen", Args: json.RawMessage(`{}`)}},
			Native:         json.RawMessage(`[{"type":"thinking","thinking":"","signature":"EqQBCkYIBxgCKkA"},{"type":"text","text":"Checking again."},{"type":"tool_use","id":"toolu_01AbC","name":"relay_screen","input":{}}]`),
			NativeProtocol: ProtocolAnthropic, NativeHost: host, NativeModel: "claude-opus-5-5"}
		own2 = Message{Role: "assistant", ToolCalls: []event.ToolCall{{ID: "toolu_01XyZ", Name: "send_message", Args: json.RawMessage(`{"text":"Codex is done."}`)}},
			Native:         json.RawMessage(`[{"type":"thinking","thinking":"","signature":"EqQBCkYIBxgCKkB"},{"type":"tool_use","id":"toolu_01XyZ","name":"send_message","input":{"text":"Codex is done."}}]`),
			NativeProtocol: ProtocolAnthropic, NativeHost: host, NativeModel: "claude-opus-5-5-20261101"}
	case ProtocolResponses:
		own1 = Message{Role: "assistant", Text: "Checking again.", ToolCalls: []event.ToolCall{{ID: "call_r1", Name: "relay_screen", Args: json.RawMessage(`{}`)}},
			Native:         json.RawMessage(`[{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"gAAAAB1"},{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"Checking again."}]},{"type":"function_call","id":"fc_1","call_id":"call_r1","name":"relay_screen","arguments":"{}"}]`),
			NativeProtocol: ProtocolResponses, NativeHost: host, NativeModel: "gpt-6"}
		own2 = Message{Role: "assistant", ToolCalls: []event.ToolCall{{ID: "call_r2", Name: "send_message", Args: json.RawMessage(`{"text":"Codex is done."}`)}},
			Native:         json.RawMessage(`[{"type":"reasoning","id":"rs_2","summary":[{"type":"summary_text","text":"report"}],"encrypted_content":"gAAAAB2"},{"type":"function_call","id":"fc_2","call_id":"call_r2","name":"send_message","arguments":"{\"text\":\"Codex is done.\"}"}]`),
			NativeProtocol: ProtocolResponses, NativeHost: host, NativeModel: "gpt-6-2026-03-01"}
	case ProtocolChat:
		// Kimi gives every reply the same ids and is sent them back as it
		// wrote them.
		own1 = Message{Role: "assistant", Text: "Checking again.", ToolCalls: []event.ToolCall{{ID: "functions.relay_screen:0", Name: "relay_screen", Args: json.RawMessage(`{}`)}},
			Native: json.RawMessage(`{"reasoning_content":"look again"}`), NativeProtocol: ProtocolChat, NativeHost: host, NativeModel: "kimi-k3"}
		own2 = Message{Role: "assistant", ToolCalls: []event.ToolCall{{ID: "functions.relay_screen:0", Name: "send_message", Args: json.RawMessage(`{"text":"Codex is done."}`)}},
			Native: json.RawMessage(`{"reasoning_content":"tell them"}`), NativeProtocol: ProtocolChat, NativeHost: host, NativeModel: "kimi-k3"}
		foreign.NativeProtocol, foreign.Native = ProtocolAnthropic, json.RawMessage(`[{"type":"thinking","thinking":"","signature":"sig"},{"type":"text","text":"Looking at the screen."},{"type":"tool_use","id":"call_q7Hk2","name":"relay_screen","input":{}}]`)
	}
	ownResult := func(m Message, out string) Message {
		return Message{Role: "tool", Results: []ToolResult{{CallID: m.ToolCalls[0].ID, Name: m.ToolCalls[0].Name, Output: out}}}
	}
	return []Message{
		{Role: "user", Text: "[10:00:00] <user> is codex done?"},
		foreign, foreignResult,
		{Role: "user", Text: "[10:01:00 You now run on another model (settings).]"},
		{Role: "user", Text: "[10:01:05] <user> check again"},
		own1, ownResult(own1, "codex> idle"),
		{Role: "user", Text: "[10:02:00] <Codex> Finished."},
		own2, ownResult(own2, "sent"),
		{Role: "user", Text: "[10:02:10] <user> thanks"},
	}
}

var goldenEndpoints = map[string]Endpoint{
	ProtocolAnthropic: {Protocol: ProtocolAnthropic, Model: "claude-opus-5-5", ReasoningEffort: "xhigh", MaxTokens: 32768},
	ProtocolResponses: {Protocol: ProtocolResponses, Model: "gpt-6", ReasoningEffort: "high", ReplayReasoning: true, MaxTokens: 32768},
	ProtocolChat:      {Protocol: ProtocolChat, Model: "kimi-k3", ReasoningEffort: "high", ReplayReasoning: true, MaxTokens: 32768},
}

// An ordinary session's requests are the bytes they were before ids,
// models and empty blocks were looked at: each one is recorded in
// testdata. Regenerate
// the file only on purpose (R3WIRE_UPDATE=1), knowing that it changes what
// running sessions send.
func TestAnOrdinarySessionSendsTheSameBytesAsBefore(t *testing.T) {
	var got bytes.Buffer
	for _, protocol := range []string{ProtocolAnthropic, ProtocolResponses, ProtocolChat} {
		w := newWireCapture(t, protocol)
		conv := ordinaryWireSession(protocol, w.srv.URL)
		// One request per point where the conversation grew, each ending
		// with the tail a look carries.
		for n := 1; n <= len(conv); n++ {
			if conv[n-1].Role == "assistant" {
				continue // a request never ends on the model's own turn
			}
			msgs := append(append([]Message(nil), conv[:n]...), Message{Role: "user", Text: fmt.Sprintf("[eagent] look %d", n)})
			body := w.send(t, goldenEndpoints[protocol], msgs)
			fmt.Fprintf(&got, "%s %d %s\n", protocol, n, body)
		}
	}
	path := filepath.Join("testdata", "r3wire-ordinary.golden")
	if os.Getenv("R3WIRE_UPDATE") == "1" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	gotLines, wantLines := strings.Split(got.String(), "\n"), strings.Split(string(want), "\n")
	if len(gotLines) != len(wantLines) {
		t.Fatalf("%d requests, want %d", len(gotLines), len(wantLines))
	}
	for i := range wantLines {
		if gotLines[i] != wantLines[i] {
			t.Fatalf("request %d changed:\nwas %s\nnow %s", i, wantLines[i], gotLines[i])
		}
	}
}
