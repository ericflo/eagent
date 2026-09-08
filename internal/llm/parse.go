package llm

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/ericflo/eagent/internal/event"
)

var argKeyValue = regexp.MustCompile(`(?s)<arg_key>(.*?)</arg_key>\s*<arg_value>(.*?)</arg_value>`)

// toolCallBlocks finds every <tool_call> block and returns, per block, the
// indices {start, end, bodyStart, bodyEnd}. A block ends at its
// </tool_call>, at the next <tool_call> (an unterminated block must not
// swallow the one after it), or at the end of the text. A tag quoted inside
// a closed <arg_value> span is argument text, not markup.
func toolCallBlocks(text string) [][]int {
	const open = "<tool_call>"
	var out [][]int
	pos := 0
	for pos < len(text) {
		i := strings.Index(text[pos:], open)
		if i < 0 {
			break
		}
		start := pos + i
		bodyStart := start + len(open)
		bodyEnd, next := scanBlock(text[bodyStart:])
		out = append(out, []int{start, bodyStart + next, bodyStart, bodyStart + bodyEnd})
		pos = bodyStart + next
	}
	return out
}

func indexFrom(s, sub string, from int) int {
	i := strings.Index(s[from:], sub)
	if i < 0 {
		return -1
	}
	return from + i
}

// scanBlock returns the end of a block body and the offset just past the
// block. A closed <arg_value>…</arg_value> span is skipped whole, so a tag
// inside it neither ends the block nor starts a new one; a truncated value
// shields nothing.
func scanBlock(rest string) (bodyEnd, next int) {
	const open, closeTag, av, ave, ak = "<tool_call>", "</tool_call>", "<arg_value>", "</arg_value>", "<arg_key>"
	// Every search is cached and refreshed only once the cursor has passed
	// its result: the cursor only moves forward, so each tag is searched for
	// once per occurrence and the scan is linear in the text.
	const unknown = -2
	cC, cN, cA, cE, cK := unknown, unknown, unknown, unknown, unknown
	seek := func(cur *int, sub string, from int) int {
		switch {
		case *cur == -1:
			return -1 // nothing ahead of an earlier position; nothing ahead of a later one
		case *cur >= from:
			return *cur
		}
		*cur = indexFrom(rest, sub, from)
		return *cur
	}
	for pos := 0; pos < len(rest); {
		c := seek(&cC, closeTag, pos)
		n := seek(&cN, open, pos)
		a := seek(&cA, av, pos)
		if a >= 0 && (c < 0 || a < c) && (n < 0 || a < n) {
			e := seek(&cE, ave, a+len(av))
			k := seek(&cK, ak, a+len(av))
			if e >= 0 && !(k >= 0 && k < e) {
				pos = e + len(ave) // a closed value: its content is text
			} else {
				pos = a + len(av) // a truncated value shields nothing
			}
			continue
		}
		if c >= 0 && (n < 0 || c < n) {
			return c, c + len(closeTag)
		}
		if n >= 0 {
			return n, n
		}
		break
	}
	return len(rest), len(rest)
}

// ParseTextToolCalls recovers tool calls that a model emitted as text instead
// of through the structured channel. GLM models occasionally leak their
// native format:
//
//	<tool_call>bash
//	<arg_key>command</arg_key>
//	<arg_value>ls</arg_value>
//	</tool_call>
//
// A JSON form (<tool_call>{"name": ..., "arguments": {...}}</tool_call>) is
// also accepted. It returns the calls and the text with the blocks removed.
func ParseTextToolCalls(text string) ([]event.ToolCall, string) {
	if !strings.Contains(text, "<tool_call>") {
		return nil, text
	}
	var calls []event.ToolCall
	matches := toolCallBlocks(text)
	if len(matches) == 0 {
		return nil, text
	}
	prefix := "text_call_" + nonce() + "_"
	var rest strings.Builder
	last := 0
	for _, m := range matches {
		rest.WriteString(text[last:m[0]])
		last = m[1]
		body := strings.TrimSpace(text[m[2]:m[3]])
		if body == "" || (!strings.Contains(body, "<arg_key>") && !strings.HasPrefix(body, "{") && strings.ContainsAny(body, " \n")) {
			// Prose that merely mentions the tag, not a call.
			rest.WriteString(text[m[0]:m[1]])
			continue
		}
		if tc, ok := parseJSONToolCall(body); ok {
			tc.ID = fmt.Sprintf("%s%d", prefix, len(calls)+1)
			calls = append(calls, tc)
			continue
		}
		// Name is the first line (or the text before the first tag).
		name := body
		if i := strings.Index(body, "<arg_key>"); i >= 0 {
			name = body[:i]
		}
		name = strings.TrimSpace(strings.SplitN(name, "\n", 2)[0])
		name = strings.Trim(name, "`\" ")
		if name == "" {
			continue
		}
		args := map[string]any{}
		for _, kv := range argKeyValue.FindAllStringSubmatch(body, -1) {
			k := strings.TrimSpace(kv[1])
			v := strings.TrimSpace(kv[2])
			args[k] = coerceValue(v)
		}
		raw, _ := json.Marshal(args)
		calls = append(calls, event.ToolCall{ID: fmt.Sprintf("%s%d", prefix, len(calls)+1), Name: name, Args: raw})
	}
	rest.WriteString(text[last:])
	return calls, strings.TrimSpace(rest.String())
}

func parseJSONToolCall(body string) (event.ToolCall, bool) {
	if !strings.HasPrefix(body, "{") {
		return event.ToolCall{}, false
	}
	var obj struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
		Params    json.RawMessage `json:"parameters"`
	}
	if err := json.Unmarshal([]byte(body), &obj); err != nil || obj.Name == "" {
		return event.ToolCall{}, false
	}
	args := obj.Arguments
	if len(args) == 0 {
		args = obj.Params
	}
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	// Arguments may be double-encoded.
	var s string
	if json.Unmarshal(args, &s) == nil && strings.HasPrefix(strings.TrimSpace(s), "{") {
		args = json.RawMessage(s)
	}
	return event.ToolCall{Name: obj.Name, Args: normalizeArgs(string(args))}, true
}

// coerceValue turns an argument string into a JSON-ish value when it is
// unambiguously a number, boolean, null, object, or array.
func coerceValue(v string) any {
	switch v {
	case "true":
		return true
	case "false":
		return false
	case "null":
		return nil
	}
	if strings.HasPrefix(v, "{") || strings.HasPrefix(v, "[") {
		var out any
		if json.Unmarshal([]byte(v), &out) == nil {
			return out
		}
	}
	var n json.Number
	if json.Unmarshal([]byte(v), &n) == nil && !strings.ContainsAny(v, " \n") {
		if f, err := n.Float64(); err == nil {
			if i, err := n.Int64(); err == nil {
				return i
			}
			return f
		}
	}
	return v
}

// RepairLeakedArgs fixes a GLM failure mode where the model's native argument
// markup leaks inside a JSON string value, e.g.
// {"tasks":"[\"t1\"]<arg_key>timeout_seconds</arg_key><arg_value>600</arg_value>"}.
// The value is cut at the first tag and the remaining key/value pairs are
// added as separate arguments. It returns true if anything changed.
func RepairLeakedArgs(obj map[string]any) bool {
	changed := false
	for k, v := range obj {
		s, ok := v.(string)
		if !ok || !strings.Contains(s, "<arg_key>") {
			continue
		}
		cut := strings.Index(s, "<arg_key>")
		head := strings.TrimSpace(s[:cut])
		obj[k] = coerceValue(head)
		for _, kv := range argKeyValue.FindAllStringSubmatch(s[cut:], -1) {
			key := strings.TrimSpace(kv[1])
			if _, exists := obj[key]; !exists {
				obj[key] = coerceValue(strings.TrimSpace(kv[2]))
			}
		}
		changed = true
	}
	return changed
}
