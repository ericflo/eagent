package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseGLMTextToolCall(t *testing.T) {
	text := "Let me check.\n<tool_call>bash\n<arg_key>command</arg_key>\n<arg_value>ls -la</arg_value>\n<arg_key>wait_seconds</arg_key>\n<arg_value>5</arg_value>\n</tool_call>"
	calls, rest := ParseTextToolCalls(text)
	if len(calls) != 1 {
		t.Fatalf("calls = %d", len(calls))
	}
	if calls[0].Name != "bash" {
		t.Fatalf("name = %s", calls[0].Name)
	}
	var args map[string]any
	if err := json.Unmarshal(calls[0].Args, &args); err != nil {
		t.Fatal(err)
	}
	if args["command"] != "ls -la" || args["wait_seconds"] != float64(5) {
		t.Fatalf("args = %v", args)
	}
	if rest != "Let me check." {
		t.Fatalf("rest = %q", rest)
	}
}

func TestParseLeakedPollCall(t *testing.T) {
	// The exact shape seen in the muse logs: an unterminated block.
	text := `<tool_call>bash_poll
<arg_key>handle</arg_key>
<arg_value>bash-3</arg_value>`
	calls, _ := ParseTextToolCalls(text)
	if len(calls) != 1 || calls[0].Name != "bash_poll" {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestParseJSONToolCall(t *testing.T) {
	text := `<tool_call>{"name":"write_file","arguments":{"path":"a.txt","content":"x"}}</tool_call>`
	calls, _ := ParseTextToolCalls(text)
	if len(calls) != 1 || calls[0].Name != "write_file" {
		t.Fatalf("calls = %+v", calls)
	}
	obj, err := ArgsObject(calls[0].Args)
	if err != nil || obj["path"] != "a.txt" {
		t.Fatalf("args = %v %v", obj, err)
	}
}

func TestNoToolCallInPlainText(t *testing.T) {
	calls, rest := ParseTextToolCalls("Just a sentence about <tools>.")
	if calls != nil || rest != "Just a sentence about <tools>." {
		t.Fatal("false positive")
	}
}

func TestMalformedArgsAreReported(t *testing.T) {
	raw := normalizeArgs(`{"path": "a.txt", "content": "unterminated`)
	if _, err := ArgsObject(raw); err == nil {
		t.Fatal("expected an error for truncated JSON")
	}
	if replay := argsString(raw); !json.Valid([]byte(replay)) || !strings.Contains(replay, "_malformed") {
		t.Fatalf("replay must be valid JSON carrying the malformed text, got %s", replay)
	}
	if _, err := ArgsObject(normalizeArgs("")); err != nil {
		t.Fatal("empty args should be an empty object")
	}
}

func TestAPIErrorClassification(t *testing.T) {
	quota := &APIError{Status: 429, Body: `{"error":{"message":"You have no credits remaining."}}`, Code: "credit_balance_exhausted"}
	if quota.Retryable() || !quota.Unroutable() {
		t.Fatal("quota exhaustion must fall back, not retry")
	}
	rate := &APIError{Status: 429, Body: "rate limit"}
	if !rate.Retryable() || rate.Unroutable() {
		t.Fatal("rate limit must retry")
	}
	ctx := &APIError{Status: 400, Body: "This model's maximum context length is 131072 tokens"}
	if !ctx.ContextOverflow() {
		t.Fatal("context overflow not detected")
	}
	if !(&APIError{Status: 503, Body: "no available server"}).Retryable() {
		t.Fatal("503 must retry")
	}
}
