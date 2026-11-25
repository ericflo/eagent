package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// wireBody decodes a captured request body.
func wireBody(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// anthropicWire sends conv to an Anthropic endpoint for model and returns
// the messages that went out.
func anthropicWire(t *testing.T, model string, conv func(host string) []Message) []any {
	t.Helper()
	w := newWireCapture(t, ProtocolAnthropic)
	body := wireBody(t, w.send(t, Endpoint{Protocol: ProtocolAnthropic, Model: model, ReasoningEffort: "xhigh"}, conv(w.srv.URL)))
	return body["messages"].([]any)
}

// responsesWire sends conv to a Responses endpoint for model and returns
// the input items that went out.
func responsesWire(t *testing.T, model string, conv func(host string) []Message) []any {
	t.Helper()
	w := newWireCapture(t, ProtocolResponses)
	body := wireBody(t, w.send(t, Endpoint{Protocol: ProtocolResponses, Model: model, ReasoningEffort: "high", ReplayReasoning: true}, conv(w.srv.URL)))
	return body["input"].([]any)
}

// blocksOf lists every content block of an Anthropic conversation, with
// the role of the message it is in.
func blocksOf(msgs []any) (roles []string, blocks []map[string]any) {
	for _, m := range msgs {
		mm := m.(map[string]any)
		for _, b := range mm["content"].([]any) {
			roles = append(roles, mm["role"].(string))
			blocks = append(blocks, b.(map[string]any))
		}
	}
	return
}

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// --- Which model's native items are replayed -------------------------------

// A gateway that serves several vendors' models over the Anthropic protocol
// hands each its own signatures. A Claude model cannot read another
// vendor's signature, so that turn goes to it rebuilt from its text and
// calls, as it would at another host.
func TestAClaudeModelIsNotSentAnotherVendorsSignedThinking(t *testing.T) {
	msgs := anthropicWire(t, "claude-opus-5-5", func(host string) []Message {
		return []Message{
			{Role: "user", Text: "status?"},
			{Role: "assistant", Text: "Looking.", ToolCalls: []event.ToolCall{{ID: "toolu_k1", Name: "relay_screen", Args: json.RawMessage(`{}`)}},
				Native:         json.RawMessage(`[{"type":"thinking","thinking":"hm","signature":"moonshot-signature"},{"type":"text","text":"Looking."},{"type":"tool_use","id":"toolu_k1","name":"relay_screen","input":{}}]`),
				NativeProtocol: ProtocolAnthropic, NativeHost: host, NativeModel: "kimi-k3"},
			{Role: "tool", Results: []ToolResult{{CallID: "toolu_k1", Name: "relay_screen", Output: "idle"}}},
			{Role: "user", Text: "[eagent] look"},
		}
	})
	got := toJSON(msgs)
	if strings.Contains(got, "moonshot-signature") || strings.Contains(got, `"thinking"`) {
		t.Fatalf("another vendor's thinking went to Claude: %s", got)
	}
	if !strings.Contains(got, `"id":"toolu_k1"`) || !strings.Contains(got, `"text":"Looking."`) {
		t.Fatalf("the turn was not rebuilt from its text and calls: %s", got)
	}
}

// Between Claude models the API itself leaves out a block the model cannot
// read, without failing the request, and some models read others' blocks:
// those turns go out unchanged.
func TestSwitchingBetweenClaudeModelsKeepsTheirThinking(t *testing.T) {
	msgs := anthropicWire(t, "claude-opus-5-5", func(host string) []Message {
		return []Message{
			{Role: "user", Text: "status?"},
			{Role: "assistant", ToolCalls: []event.ToolCall{{ID: "toolu_f1", Name: "relay_screen", Args: json.RawMessage(`{}`)}},
				Native:         json.RawMessage(`[{"type":"thinking","thinking":"","signature":"fable-signature"},{"type":"tool_use","id":"toolu_f1","name":"relay_screen","input":{}}]`),
				NativeProtocol: ProtocolAnthropic, NativeHost: host, NativeModel: "claude-fable-5-1"},
			{Role: "tool", Results: []ToolResult{{CallID: "toolu_f1", Name: "relay_screen", Output: "idle"}}},
		}
	})
	if !strings.Contains(toJSON(msgs), "fable-signature") {
		t.Fatalf("a Claude model's thinking was taken out of the conversation: %s", toJSON(msgs))
	}
}

func responsesTurn(host, model string) []Message {
	return []Message{
		{Role: "user", Text: "status?"},
		{Role: "assistant", ToolCalls: []event.ToolCall{{ID: "call_a1", Name: "relay_screen", Args: json.RawMessage(`{}`)}},
			Native:         json.RawMessage(`[{"type":"reasoning","id":"rs_a1","summary":[],"encrypted_content":"gAAAA-astra"},{"type":"function_call","id":"fc_a1","call_id":"call_a1","name":"relay_screen","arguments":"{}"}]`),
			NativeProtocol: ProtocolResponses, NativeHost: host, NativeModel: model},
		{Role: "tool", Results: []ToolResult{{CallID: "call_a1", Name: "relay_screen", Output: "idle"}}},
	}
}

// Encrypted reasoning and item ids belong to the model that made them.
// After a switch to another model at the same host, that model's turns are
// rebuilt, which every model takes.
func TestSwitchingModelsOnResponsesRebuildsTheOtherModelsTurns(t *testing.T) {
	for _, recorded := range []string{"gpt-6-astra", "gpt-6.1-sol-mini"} {
		items := responsesWire(t, "gpt-6.1-sol", func(host string) []Message { return responsesTurn(host, recorded) })
		got := toJSON(items)
		if strings.Contains(got, "gAAAA-astra") || strings.Contains(got, "fc_a1") {
			t.Fatalf("%s's native items went to gpt-6.1-sol: %s", recorded, got)
		}
		if !strings.Contains(got, `"call_id":"call_a1"`) {
			t.Fatalf("the call was not rebuilt: %s", got)
		}
	}
}

// A response names the model it ran, which can be a dated variant of the
// one asked for, or carry the router's vendor prefix. That is the same
// model, and its reasoning goes back to it.
func TestADatedResponseModelStillReplaysToTheModelAskedFor(t *testing.T) {
	for _, c := range []struct{ asked, recorded string }{
		{"gpt-6.1-sol", "gpt-6.1-sol-2026-09-01"},
		{"gpt-6.1-sol", "gpt-6.1-sol-20260901"},
		{"openai/gpt-6.1-sol", "openai/gpt-6.1-sol-2026-09-01"},
		{"openai/gpt-6.1-sol", "gpt-6.1-sol"},
		{"GPT-6.1-Sol", "gpt-6.1-sol"},
	} {
		items := responsesWire(t, c.asked, func(host string) []Message { return responsesTurn(host, c.recorded) })
		if !strings.Contains(toJSON(items), "gAAAA-astra") {
			t.Fatalf("asked %s, response named %s: the reasoning was not replayed", c.asked, c.recorded)
		}
	}
}

// A record that does not say which model made a turn replays it as before.
func TestATurnWithoutAModelReplaysAsBefore(t *testing.T) {
	items := responsesWire(t, "gpt-6.1-sol", func(host string) []Message { return responsesTurn(host, "") })
	if !strings.Contains(toJSON(items), "gAAAA-astra") {
		t.Fatal("a turn with no model was rebuilt")
	}
	msgs := anthropicWire(t, "claude-opus-5-5", func(host string) []Message {
		return []Message{
			{Role: "user", Text: "q"},
			{Role: "assistant", Native: json.RawMessage(`[{"type":"thinking","thinking":"","signature":"old-sig"},{"type":"text","text":"a"}]`), NativeProtocol: ProtocolAnthropic, NativeHost: host},
			{Role: "user", Text: "q2"},
		}
	})
	if !strings.Contains(toJSON(msgs), "old-sig") {
		t.Fatal("a turn with no model was rebuilt")
	}
}

// --- Tool-call ids ---------------------------------------------------------

// Kimi gives every reply the same ids, with dots and colons in them.
func kimiTurns() []Message {
	call := func(id, name string) event.ToolCall {
		return event.ToolCall{ID: id, Name: name, Args: json.RawMessage(`{}`)}
	}
	return []Message{
		{Role: "user", Text: "status?"},
		{Role: "assistant", ToolCalls: []event.ToolCall{call("functions.relay_screen:0", "relay_screen")}, NativeProtocol: ProtocolChat, NativeModel: "kimi-k3", NativeHost: "https://api.moonshot.ai/v1"},
		{Role: "tool", Results: []ToolResult{{CallID: "functions.relay_screen:0", Name: "relay_screen", Output: "first screen"}}},
		{Role: "user", Text: "again"},
		{Role: "assistant", ToolCalls: []event.ToolCall{call("functions.relay_screen:0", "relay_screen"), call("functions.send_message:1", "send_message")}, NativeProtocol: ProtocolChat, NativeModel: "kimi-k3", NativeHost: "https://api.moonshot.ai/v1"},
		{Role: "tool", Results: []ToolResult{
			{CallID: "functions.relay_screen:0", Name: "relay_screen", Output: "second screen"},
			{CallID: "functions.send_message:1", Name: "send_message", Output: "sent"},
		}},
		{Role: "user", Text: "[10:05] <user> switched you to Claude"},
	}
}

var anthropicID = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// An id Anthropic would refuse (characters outside its pattern, or one used
// earlier in the same request) is replaced, in the call and its result
// alike, so the conversation still pairs up.
func TestReusedAndForeignToolCallIDsAreRenumberedForAnthropic(t *testing.T) {
	msgs := anthropicWire(t, "claude-opus-5-5", func(string) []Message { return kimiTurns() })
	_, blocks := blocksOf(msgs)
	seen := map[string]bool{}
	var calls []string
	for _, b := range blocks {
		switch b["type"] {
		case "tool_use":
			id := b["id"].(string)
			if !anthropicID.MatchString(id) || seen[id] {
				t.Fatalf("tool_use id %q would be refused: %s", id, toJSON(msgs))
			}
			seen[id] = true
			calls = append(calls, id)
		case "tool_result":
			if len(calls) == 0 || b["tool_use_id"] != calls[0] {
				t.Fatalf("result %v does not answer the call it follows (%v): %s", b["tool_use_id"], calls, toJSON(msgs))
			}
			calls = calls[1:]
		}
	}
	if len(seen) != 3 {
		t.Fatalf("%d calls, want 3", len(seen))
	}
	// The results kept their outputs in order.
	got := toJSON(msgs)
	if strings.Index(got, "first screen") > strings.Index(got, "second screen") || !strings.Contains(got, "sent") {
		t.Fatalf("results out of order: %s", got)
	}
}

// Responses refuses a call_id used twice in one request. The first use goes
// out as written; a later one is replaced, in the call and its output.
func TestReusedToolCallIDsAreRenumberedForResponses(t *testing.T) {
	items := responsesWire(t, "gpt-6.1-sol", func(string) []Message { return kimiTurns() })
	seen := map[string]bool{}
	var calls []string
	for _, it := range items {
		m := it.(map[string]any)
		switch m["type"] {
		case "function_call":
			id := m["call_id"].(string)
			if id == "" || seen[id] {
				t.Fatalf("call_id %q is used twice: %s", id, toJSON(items))
			}
			seen[id] = true
			calls = append(calls, id)
		case "function_call_output":
			if len(calls) == 0 || m["call_id"] != calls[0] {
				t.Fatalf("output %v does not answer the call it follows (%v): %s", m["call_id"], calls, toJSON(items))
			}
			calls = calls[1:]
		}
	}
	if len(seen) != 3 || !seen["functions.relay_screen:0"] || !seen["functions.send_message:1"] {
		t.Fatalf("calls = %v; the first use of each id should go out as written", seen)
	}
}

// withoutBreakpoints drops cache_control from a rendered conversation:
// breakpoints move from call to call by design, the content may not.
func withoutBreakpoints(msgs []any) string {
	s := toJSON(msgs)
	return regexp.MustCompile(`"cache_control":\{"type":"ephemeral"\},?`).ReplaceAllString(s, "")
}

// The replaced ids depend only on what came before them: the same record
// renders the same bytes every time, and a longer conversation starts with
// the bytes of the shorter one.
func TestRenumberedIDsAreTheSameOnEveryRequest(t *testing.T) {
	base := kimiTurns()
	longer := append(append([]Message(nil), base...),
		Message{Role: "assistant", ToolCalls: []event.ToolCall{{ID: "functions.relay_screen:0", Name: "relay_screen", Args: json.RawMessage(`{}`)}}, NativeProtocol: ProtocolChat, NativeModel: "kimi-k3"},
		Message{Role: "tool", Results: []ToolResult{{CallID: "functions.relay_screen:0", Name: "relay_screen", Output: "third"}}},
	)
	a1 := anthropicWire(t, "claude-opus-5-5", func(string) []Message { return base })
	a2 := anthropicWire(t, "claude-opus-5-5", func(string) []Message { return base })
	a3 := anthropicWire(t, "claude-opus-5-5", func(string) []Message { return longer })
	if toJSON(a1) != toJSON(a2) {
		t.Fatalf("the same record rendered two ways:\n%s\n%s", toJSON(a1), toJSON(a2))
	}
	short := withoutBreakpoints(a1[:len(a1)-1]) // the last message grows by merging
	if long := withoutBreakpoints(a3); !strings.HasPrefix(long, strings.TrimSuffix(short, "]")) {
		t.Fatalf("the longer conversation does not start with the shorter one:\n%s\n%s", short, long)
	}
	r1 := responsesWire(t, "gpt-6.1-sol", func(string) []Message { return base })
	r3 := responsesWire(t, "gpt-6.1-sol", func(string) []Message { return longer })
	if !strings.HasPrefix(toJSON(r3), strings.TrimSuffix(toJSON(r1), "]")) {
		t.Fatalf("the longer conversation does not start with the shorter one:\n%s\n%s", toJSON(r1), toJSON(r3))
	}
}

// A provider that reuses its own ids is sent them back as it wrote them on
// Chat Completions, where that has always worked.
func TestChatSendsAProvidersOwnReusedIDsBackAsWritten(t *testing.T) {
	w := newWireCapture(t, ProtocolChat)
	body := wireBody(t, w.send(t, Endpoint{Protocol: ProtocolChat, Model: "kimi-k3"}, kimiTurns()))
	if n := strings.Count(toJSON(body["messages"]), `"functions.relay_screen:0"`); n != 4 {
		t.Fatalf("%d uses of the reused id, want 4 (two calls, two results): %s", n, toJSON(body["messages"]))
	}
}

// --- Anthropic thinking ----------------------------------------------------

// A reply cut off by the output limit while thinking ends with a thinking
// block that never got its signature. Anthropic refuses an unsigned block,
// so it is not kept.
func TestAnUnsignedThinkingBlockIsNotKept(t *testing.T) {
	stream := sseServer(t, func(w http.ResponseWriter, _ map[string]any) {
		for _, f := range []string{
			`{"type":"message_start","message":{"model":"claude-opus-5-5","usage":{"input_tokens":5}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"relay_screen"}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{}"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"content_block_start","index":1,"content_block":{"type":"thinking"}}`,
			`{"type":"content_block_delta","index":1,"delta":{"type":"thinking_delta","thinking":"and then"}}`,
			`{"type":"message_delta","delta":{"stop_reason":"max_tokens"},"usage":{"output_tokens":9}}`,
			`{"type":"message_stop"}`,
		} {
			fmt.Fprintf(w, "data: %s\n\n", f)
		}
	})
	defer stream.Close()
	nonStream := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		fmt.Fprint(rw, `{"type":"message","model":"claude-opus-5-5","stop_reason":"max_tokens","content":[{"type":"tool_use","id":"toolu_1","name":"relay_screen","input":{}},{"type":"thinking","thinking":"and then"}]}`)
	}))
	defer nonStream.Close()
	for name, url := range map[string]string{"streamed": stream.URL, "not streamed": nonStream.URL} {
		c := NewClient(Endpoint{Protocol: ProtocolAnthropic, BaseURL: url, Model: "claude-opus-5-5", ReasoningEffort: "xhigh"})
		c.MaxAttempts = 1
		resp, err := c.Complete(context.Background(), Request{Messages: []Message{{Role: "user", Text: "go"}}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(resp.Native), "thinking") || !strings.Contains(string(resp.Native), "toolu_1") {
			t.Fatalf("%s: native = %s", name, resp.Native)
		}
	}
}

// A record written before that holds an unsigned block renders without it.
func TestAnOldRecordsUnsignedThinkingIsNotReplayed(t *testing.T) {
	msgs := anthropicWire(t, "claude-opus-5-5", func(host string) []Message {
		return []Message{
			{Role: "user", Text: "go"},
			{Role: "assistant", ToolCalls: []event.ToolCall{{ID: "toolu_1", Name: "relay_screen", Args: json.RawMessage(`{}`)}},
				Native:         json.RawMessage(`[{"type":"thinking","thinking":"","signature":"good"},{"type":"tool_use","id":"toolu_1","name":"relay_screen","input":{}},{"type":"thinking","thinking":"and then","signature":""}]`),
				NativeProtocol: ProtocolAnthropic, NativeHost: host, NativeModel: "claude-opus-5-5"},
			{Role: "tool", Results: []ToolResult{{CallID: "toolu_1", Name: "relay_screen", Output: "idle"}}},
		}
	})
	got := toJSON(msgs)
	if strings.Contains(got, "and then") || !strings.Contains(got, `"signature":"good"`) {
		t.Fatalf("assistant turn = %s", got)
	}
}

// A reply that is only thinking, cut off by the output limit, is sent back
// for another try. Sent as a thinking block alone it would be refused; it
// goes back as a turn with no output.
func TestAThinkingOnlyReplyIsSentBackAsNoOutput(t *testing.T) {
	for _, native := range []string{
		`[{"type":"thinking","thinking":"long thought"}]`,
		`[{"type":"thinking","thinking":"long thought","signature":"sig"}]`,
	} {
		msgs := anthropicWire(t, "claude-opus-5-5", func(host string) []Message {
			return []Message{
				{Role: "user", Text: "go"},
				{Role: "assistant", Native: json.RawMessage(native), NativeProtocol: ProtocolAnthropic, NativeHost: host, NativeModel: "claude-opus-5-5"},
				{Role: "user", Text: "[eagent] Your reply was cut off by the output limit."},
			}
		})
		if got := withoutBreakpoints(msgs[1:2]); got != `[{"content":[{"text":"(no output)","type":"text"}],"role":"assistant"}]` {
			t.Fatalf("%s went back as %s", native, got)
		}
	}
}

// Another vendor's model behind the Anthropic protocol may write thinking
// without a signature and expect it back; that stays as it was.
func TestUnsignedThinkingStaysForAModelThatIsNotClaude(t *testing.T) {
	msgs := anthropicWire(t, "kimi-k3", func(host string) []Message {
		return []Message{
			{Role: "user", Text: "go"},
			{Role: "assistant", Native: json.RawMessage(`[{"type":"thinking","thinking":"plan"},{"type":"text","text":"ok"}]`), NativeProtocol: ProtocolAnthropic, NativeHost: host, NativeModel: "kimi-k3"},
			{Role: "user", Text: "next"},
		}
	})
	if !strings.Contains(toJSON(msgs), `"thinking":"plan"`) {
		t.Fatalf("thinking was dropped for a model that is not Claude: %s", toJSON(msgs))
	}
}

// --- Responses reasoning ---------------------------------------------------

// With store off, a reasoning item is replayed by its encrypted content; one
// without it (made while replay was off) would be looked up by id and not
// found, so it is not sent.
func TestReasoningWithoutEncryptedContentIsNotReplayed(t *testing.T) {
	items := responsesWire(t, "gpt-6", func(host string) []Message {
		return []Message{
			{Role: "user", Text: "go"},
			{Role: "assistant", ToolCalls: []event.ToolCall{{ID: "call_1", Name: "relay_screen", Args: json.RawMessage(`{}`)}},
				Native:         json.RawMessage(`[{"type":"reasoning","id":"rs_plain","summary":[{"type":"summary_text","text":"look"}]},{"type":"function_call","id":"fc_1","call_id":"call_1","name":"relay_screen","arguments":"{}"}]`),
				NativeProtocol: ProtocolResponses, NativeHost: host, NativeModel: "gpt-6"},
			{Role: "tool", Results: []ToolResult{{CallID: "call_1", Name: "relay_screen", Output: "idle"}}},
		}
	})
	got := toJSON(items)
	if strings.Contains(got, "rs_plain") || !strings.Contains(got, `"id":"fc_1"`) {
		t.Fatalf("input = %s", got)
	}
}

// --- Empty blocks ----------------------------------------------------------

// A blank screen comes back as an empty result. A cache breakpoint may not
// sit on an empty block, so it goes on the block before.
func TestAnEmptyToolResultCarriesNoBreakpoint(t *testing.T) {
	for _, tail := range []bool{false, true} {
		msgs := anthropicWire(t, "claude-opus-5-5", func(host string) []Message {
			conv := []Message{
				{Role: "user", Text: "go"},
				{Role: "assistant", ToolCalls: []event.ToolCall{{ID: "toolu_1", Name: "relay_screen", Args: json.RawMessage(`{}`)}}},
				{Role: "tool", Results: []ToolResult{{CallID: "toolu_1", Name: "relay_screen", Output: ""}}},
			}
			if tail {
				conv = append(conv, Message{Role: "user", Text: "[eagent] look"})
			}
			return conv
		})
		_, blocks := blocksOf(msgs)
		marks := 0
		for _, b := range blocks {
			if b["cache_control"] == nil {
				continue
			}
			marks++
			if b["type"] == "tool_result" && strings.TrimSpace(fmt.Sprint(b["content"])) == "" {
				t.Fatalf("a breakpoint is on an empty result: %s", toJSON(msgs))
			}
		}
		if marks != 2 {
			t.Fatalf("%d breakpoints, want 2: %s", marks, toJSON(msgs))
		}
	}
}

// Text that is only white space is not a block Anthropic takes: a rebuilt
// turn of "\n\n" and a call goes out as the call, and a blank user message
// as "(empty)".
func TestWhiteSpaceOnlyTextIsNotSentAsABlock(t *testing.T) {
	msgs := anthropicWire(t, "claude-opus-5-5", func(host string) []Message {
		return []Message{
			{Role: "user", Text: " \n"},
			{Role: "assistant", Text: "\n\n", ToolCalls: []event.ToolCall{{ID: "call_1", Name: "relay_screen", Args: json.RawMessage(`{}`)}}, NativeProtocol: ProtocolChat},
			{Role: "tool", Results: []ToolResult{{CallID: "call_1", Name: "relay_screen", Output: "idle"}}},
			{Role: "assistant", Text: "  "},
			{Role: "user", Text: "next"},
		}
	})
	_, blocks := blocksOf(msgs)
	for _, b := range blocks {
		if b["type"] == "text" && strings.TrimSpace(b["text"].(string)) == "" {
			t.Fatalf("a blank text block went out: %s", toJSON(msgs))
		}
	}
	if !strings.Contains(toJSON(msgs), `"text":"(empty)"`) || !strings.Contains(toJSON(msgs), `"text":"(no output)"`) {
		t.Fatalf("blank messages were not given their placeholders: %s", toJSON(msgs))
	}
}
