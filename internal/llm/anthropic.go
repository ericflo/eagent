package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/ericflo/eagent/internal/event"
)

// anthropic implements the Anthropic Messages API with streaming and prompt
// caching. Thinking blocks are kept in Native and replayed verbatim.
func (c *Client) anthropic(ctx context.Context, req Request, obs *Observer) (*Response, error) {
	return c.anthropicTry(ctx, req, obs, true)
}

// anthropicTry makes one call; when mayProbe is set and the model rejects
// the thinking flavour, it flips the flavour and tries once more (so every
// call, not just one per client, gets its retry).
func (c *Client) anthropicTry(ctx context.Context, req Request, obs *Observer, mayProbe bool) (*Response, error) {
	maxTokens := c.maxTokens(req)
	messages := anthropicMessagesFor(req, c.Endpoint.BaseURL, c.Endpoint.Model)
	head, _ := json.Marshal([]any{c.Endpoint.Model, req.System, req.Tools})
	cached := anthropicBridge(messages, head, c.anthropicCached.known())
	body := map[string]any{
		"model":      c.Endpoint.Model,
		"messages":   messages,
		"stream":     true,
		"max_tokens": maxTokens,
	}
	if req.System != "" {
		body["system"] = []map[string]any{{
			"type":          "text",
			"text":          req.System,
			"cache_control": map[string]any{"type": "ephemeral"},
		}}
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, 0, len(req.Tools))
		for i, t := range req.Tools {
			tool := map[string]any{
				"name":         t.Name,
				"description":  t.Description,
				"input_schema": t.Parameters,
			}
			// Tools are rendered before the system prompt, so the
			// breakpoint at its end covers them too. They get one of the
			// four a request may carry only when there is no system prompt.
			if i == len(req.Tools)-1 && req.System == "" {
				tool["cache_control"] = map[string]any{"type": "ephemeral"}
			}
			tools = append(tools, tool)
		}
		body["tools"] = tools
	}
	thinking := false
	budgeted := c.anthropicBudgeted.Load() // the flavour this call sends
	if e := c.Endpoint.ReasoningEffort; e != "" && e != "none" {
		thinking = true
		if budgeted {
			// Older models: explicit budget.
			budget, known := map[string]int{"minimal": 1024, "low": 2048, "medium": 8192, "high": 24576, "xhigh": 32768, "max": 49152}[e]
			if !known {
				// Never quietly turn an unknown effort into medium.
				return nil, fmt.Errorf("reasoning effort %q is not one this model accepts (none, minimal, low, medium, high, xhigh, max)", e)
			}
			// The budget must be at least 1024 and strictly below max_tokens;
			// when the ceiling leaves no room, send no thinking at all rather
			// than a request the API rejects.
			budget = max(min(budget, maxTokens/2), 1024)
			if budget >= maxTokens {
				thinking = false
			} else {
				body["thinking"] = map[string]any{"type": "enabled", "budget_tokens": budget}
			}
		} else {
			// Current models: adaptive thinking steered by effort.
			body["thinking"] = map[string]any{"type": "adaptive"}
			body["output_config"] = map[string]any{"effort": e}
		}
	}
	headers := map[string]string{
		"anthropic-version": "2023-06-01",
	}
	if c.Endpoint.APIKey != "" {
		headers["x-api-key"] = c.Endpoint.APIKey
	}
	resp, err := c.post(ctx, "/messages", body, headers)
	if err != nil {
		var ae *APIError
		if mayProbe && thinking && errors.As(err, &ae) && ae.Status == 400 && anthropicFlavourRejected(ae.Body, budgeted) {
			// The model may want the other thinking flavour: try it once, and
			// keep the new flavour only when it actually worked.
			c.anthropicBudgeted.CompareAndSwap(budgeted, !budgeted)
			out, rerr := c.anthropicTry(ctx, req, obs, false)
			if rerr != nil {
				c.anthropicBudgeted.CompareAndSwap(!budgeted, budgeted)
				return nil, err // the flavour was not the problem
			}
			return out, nil
		}
		return nil, err
	}
	// The request was taken: its breakpoints are where the next call's
	// cache entries are.
	c.anthropicCached.add(cached)
	if ct := resp.Header.Get("Content-Type"); strings.Contains(ct, "application/json") {
		return c.anthropicNonStreaming(resp)
	}

	type block struct {
		Type      string          `json:"type"`
		Text      string          `json:"text,omitempty"`
		ID        string          `json:"id,omitempty"`
		Name      string          `json:"name,omitempty"`
		Input     json.RawMessage `json:"input,omitempty"`
		Thinking  string          `json:"thinking,omitempty"`
		Signature string          `json:"signature,omitempty"`
		partial   strings.Builder
	}
	var blocks []*block
	out := &Response{}
	stop := ""
	stopped := false
	var failure *APIError
	err = c.readSSE(ctx, resp, func(ev sseEvent) error {
		var frame struct {
			Type    string `json:"type"`
			Index   int    `json:"index"`
			Message *struct {
				Model string `json:"model"`
				Usage struct {
					Input      int `json:"input_tokens"`
					CacheRead  int `json:"cache_read_input_tokens"`
					CacheWrite int `json:"cache_creation_input_tokens"`
				} `json:"usage"`
			} `json:"message"`
			ContentBlock *struct {
				Type  string          `json:"type"`
				ID    string          `json:"id"`
				Name  string          `json:"name"`
				Text  string          `json:"text"`
				Data  string          `json:"data"`
				Input json.RawMessage `json:"input"`
			} `json:"content_block"`
			Delta *struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
				Thinking    string `json:"thinking"`
				Signature   string `json:"signature"`
				StopReason  string `json:"stop_reason"`
			} `json:"delta"`
			Usage *struct {
				Output int `json:"output_tokens"`
			} `json:"usage"`
			Error *struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(ev.Data, &frame); err != nil {
			return nil
		}
		switch frame.Type {
		case "message_start":
			if frame.Message != nil {
				out.Model = frame.Message.Model
				out.Usage.Input = frame.Message.Usage.Input + frame.Message.Usage.CacheRead + frame.Message.Usage.CacheWrite
				out.Usage.Cached = frame.Message.Usage.CacheRead
			}
		case "content_block_start":
			if frame.ContentBlock == nil {
				return nil
			}
			b := &block{Type: frame.ContentBlock.Type, ID: frame.ContentBlock.ID, Name: frame.ContentBlock.Name, Input: frame.ContentBlock.Input}
			for len(blocks) <= frame.Index {
				blocks = append(blocks, nil)
			}
			blocks[frame.Index] = b
			if b.Type == "tool_use" {
				obs.toolCall(b.Name)
			}
			if b.Type == "text" && frame.ContentBlock.Text != "" {
				b.partial.WriteString(frame.ContentBlock.Text)
			}
			if b.Type == "redacted_thinking" {
				b.partial.WriteString(frame.ContentBlock.Data)
			}
		case "content_block_delta":
			if frame.Delta == nil || frame.Index >= len(blocks) || blocks[frame.Index] == nil {
				return nil
			}
			b := blocks[frame.Index]
			switch frame.Delta.Type {
			case "text_delta":
				b.partial.WriteString(frame.Delta.Text)
				obs.text(frame.Delta.Text)
			case "input_json_delta":
				b.partial.WriteString(frame.Delta.PartialJSON)
			case "thinking_delta":
				b.partial.WriteString(frame.Delta.Thinking)
				obs.reasoning(frame.Delta.Thinking)
			case "signature_delta":
				b.Signature += frame.Delta.Signature
			}
		case "message_delta":
			if frame.Delta != nil {
				stop = frame.Delta.StopReason
			}
			if frame.Usage != nil {
				out.Usage.Output = frame.Usage.Output
			}
		case "message_stop":
			stopped = true
		case "error":
			if frame.Error == nil {
				// An error that names nothing: overloaded, as before.
				failure = &APIError{Status: 529, Body: "stream error"}
				break
			}
			raw, _ := json.Marshal(map[string]any{"type": "error", "error": frame.Error})
			failure = streamError(nil, frame.Error.Type, frame.Error.Message, raw)
			if _, known := streamStatus[frame.Error.Type]; !known {
				failure.Status = 529
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if failure != nil {
		return nil, failure
	}
	if !stopped {
		return nil, ErrTruncatedStream
	}

	var text, reasoning strings.Builder
	native := make([]map[string]any, 0, len(blocks))
	for _, b := range blocks {
		if b == nil {
			continue
		}
		switch b.Type {
		case "text":
			b.Text = b.partial.String()
			text.WriteString(b.Text)
			if strings.TrimSpace(b.Text) != "" { // the API rejects empty text blocks on input
				native = append(native, map[string]any{"type": "text", "text": b.Text})
			}
		case "tool_use":
			raw := strings.TrimSpace(b.partial.String())
			if raw == "" && len(b.Input) > 0 {
				raw = strings.TrimSpace(string(b.Input))
			}
			if raw == "" {
				raw = "{}"
			}
			args := normalizeArgs(raw)
			out.ToolCalls = append(out.ToolCalls, event.ToolCall{ID: b.ID, Name: b.Name, Args: args})
			var input any = json.RawMessage(`{}`)
			if json.Valid([]byte(raw)) && strings.HasPrefix(raw, "{") {
				input = json.RawMessage(raw)
			}
			native = append(native, map[string]any{"type": "tool_use", "id": b.ID, "name": b.Name, "input": input})
		case "thinking":
			b.Thinking = b.partial.String()
			reasoning.WriteString(b.Thinking)
			// Cut off before its signature came: Claude would refuse it back.
			if b.Signature != "" || !claudeModel(c.Endpoint.Model) {
				native = append(native, map[string]any{"type": "thinking", "thinking": b.Thinking, "signature": b.Signature})
			}
		case "redacted_thinking":
			native = append(native, map[string]any{"type": "redacted_thinking", "data": b.partial.String()})
		}
	}
	out.Text = text.String()
	out.Reasoning = reasoning.String()
	if nat, err := json.Marshal(native); err == nil {
		out.Native = nat
	}
	switch stop {
	case "max_tokens":
		out.Stop = "length"
	case "tool_use":
		out.Stop = "tool_calls"
	default:
		if len(out.ToolCalls) > 0 {
			out.Stop = "tool_calls"
		} else {
			out.Stop = "stop"
		}
	}
	return out, nil
}

// anthropicMessages renders history for a model it does not name.
func anthropicMessages(req Request, host string) []map[string]any {
	return anthropicMessagesFor(req, host, "")
}

// anthropicMessagesFor renders history for model with strict user/assistant
// alternation and cache breakpoints on its last two blocks.
func anthropicMessagesFor(req Request, host, model string) []map[string]any {
	var msgs []map[string]any
	push := func(role string, content []map[string]any) {
		if len(msgs) > 0 && msgs[len(msgs)-1]["role"] == role {
			prev := msgs[len(msgs)-1]["content"].([]map[string]any)
			msgs[len(msgs)-1]["content"] = append(prev, content...)
			return
		}
		msgs = append(msgs, map[string]any{"role": role, "content": content})
	}
	ids := newCallIDs(anthropicAccepts)
	for i, m := range req.Messages {
		switch m.Role {
		case "user":
			text := m.Text
			if strings.TrimSpace(text) == "" { // the API refuses a blank text block
				text = "(empty)"
			}
			blocks := []map[string]any{{"type": "text", "text": text}}
			for _, im := range m.Images {
				if raw, mt := im.bytes(); raw != nil {
					blocks = append(blocks, map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": mt, "data": base64.StdEncoding.EncodeToString(raw)}})
				}
			}
			push("user", blocks)
		case "assistant":
			var content []map[string]any
			// Signed thinking blocks are bound to the account that produced
			// them; replay them only at the host that did.
			if len(m.Native) > 0 && m.NativeProtocol == ProtocolAnthropic && (m.NativeHost == "" || sameHost(m.NativeHost, host)) && anthropicReplays(m, model) {
				var native []map[string]any
				if json.Unmarshal(m.Native, &native) != nil {
					native = nil
				} else if claudeModel(model) {
					native = claudeThinking(native)
				}
				if len(native) > 0 {
					content = native
					var own []string
					for _, b := range native {
						if id, _ := b["id"].(string); b["type"] == "tool_use" {
							own = append(own, id)
						}
					}
					ids.kept(own)
				}
			}
			if content == nil {
				ids.rebuilt()
				if strings.TrimSpace(m.Text) != "" {
					content = append(content, map[string]any{"type": "text", "text": m.Text})
				}
				for j, tc := range m.ToolCalls {
					var input any = json.RawMessage(`{}`)
					if obj, err := ArgsObject(tc.Args); err == nil {
						input = obj
					}
					content = append(content, map[string]any{"type": "tool_use", "id": ids.call(i, j, tc.ID), "name": tc.Name, "input": input})
				}
			}
			if len(content) == 0 {
				content = []map[string]any{{"type": "text", "text": "(no output)"}}
			}
			push("assistant", content)
		case "tool":
			var content []map[string]any
			for _, r := range m.Results {
				block := map[string]any{"type": "tool_result", "tool_use_id": ids.result(r.CallID), "content": r.Output}
				if r.IsError {
					block["is_error"] = true
				}
				content = append(content, block)
			}
			push("user", content)
		}
	}
	// Cache breakpoints on the final block and on the one before it. A call
	// that extends the conversation hits the cache at the first. A look
	// whose last block is the end of a prompt that is not recorded (so the
	// next call carries a different one in its place) hits it at the second.
	// Adjacent user messages were merged above, so the block before the
	// last is usually inside the same message, not the message before.
	// With the one on the system prompt (which covers the tools) and the
	// one anthropicBridge may add, this stays within Anthropic's limit of
	// four breakpoints.
	stable := anthropicStable(msgs)
	for i := len(stable) - 1; i >= 0 && i >= len(stable)-2; i-- {
		stable[i]["cache_control"] = map[string]any{"type": "ephemeral"}
	}
	return msgs
}

// anthropicStable lists the content blocks of a conversation that can carry
// a cache breakpoint, in order.
func anthropicStable(msgs []map[string]any) []map[string]any {
	var stable []map[string]any
	for _, m := range msgs {
		blocks, _ := m["content"].([]map[string]any)
		for _, b := range blocks {
			if anthropicCacheable(b) {
				stable = append(stable, b)
			}
		}
	}
	return stable
}

// anthropicCacheable reports whether a content block can carry a cache
// breakpoint.
func anthropicCacheable(b map[string]any) bool {
	switch t, _ := b["type"].(string); t {
	case "thinking", "redacted_thinking":
		return false
	case "text":
		if s, _ := b["text"].(string); strings.TrimSpace(s) == "" {
			return false
		}
	case "tool_result":
		// A blank screen comes back as an empty result, and a breakpoint
		// may not sit on an empty block.
		if s, ok := b["content"].(string); ok && strings.TrimSpace(s) == "" {
			return false
		}
	}
	return true
}

// anthropicLookback is how far behind a breakpoint Anthropic looks for an
// earlier cache entry: 20 blocks, less a margin.
const anthropicLookback = 18

// anthropicBridge keeps a long step between two calls on the cache. A
// breakpoint finds an earlier entry only a short way behind it, so when
// more blocks than that were added since the last call (a chatty agent, a
// burst of events, many lookups at once) the two breakpoints at the end
// would find nothing and the whole conversation would be written to the
// cache again. A third breakpoint on the newest block that an earlier
// request left an entry at is read from the cache, and only what came
// after it is new.
//
// Where the entries are is known by what they hold, not by counting: each
// block is identified by a hash of the whole request up to and including
// it (head is what comes before the messages: model, system prompt,
// tools). Counting from the end of the last request goes wrong whenever
// that request ended in blocks that were never recorded (a reply sent back
// for another try, a look's prompt) or the conversation started afresh.
//
// known lists the prefixes earlier requests left entries at. The result is
// the prefixes this request leaves entries at, or refreshes by reading
// them, for the caller to remember once the request is taken.
func anthropicBridge(msgs []map[string]any, head []byte, known []uint64) []uint64 {
	type block struct {
		b      map[string]any
		key    uint64
		stable bool
	}
	h := fnv.New64a()
	h.Write(head)
	var blocks []block
	for _, m := range msgs {
		role, _ := m["role"].(string)
		h.Write([]byte("\x00" + role + "\x00"))
		content, _ := m["content"].([]map[string]any)
		for _, b := range content {
			cc, marked := b["cache_control"]
			delete(b, "cache_control")
			raw, _ := json.Marshal(b)
			if marked {
				b["cache_control"] = cc
			}
			h.Write(raw)
			h.Write([]byte{1})
			blocks = append(blocks, block{b: b, key: h.Sum64(), stable: anthropicCacheable(b)})
		}
	}
	reach := -1
	for i := len(blocks) - 1; i >= 0 && reach < 0; i-- {
		if !blocks[i].stable {
			continue
		}
		for _, k := range known {
			if k == blocks[i].key {
				reach = i
				break
			}
		}
	}
	if reach >= 0 && len(blocks)-1-reach > anthropicLookback {
		blocks[reach].b["cache_control"] = map[string]any{"type": "ephemeral"}
	}
	var out []uint64
	if reach >= 0 {
		out = append(out, blocks[reach].key)
	}
	for _, b := range blocks {
		if b.b["cache_control"] != nil {
			out = append(out, b.key)
		}
	}
	return out
}

// anthropicEntries remembers the prefixes the last few requests through a
// client left cache entries at (anthropicBridge). A few requests' worth, so
// that two conversations sharing a client (a dossier's lookups beside the
// session) do not push each other's out.
type anthropicEntries struct {
	mu   sync.Mutex
	keys []uint64
}

const anthropicEntriesKept = 16

func (e *anthropicEntries) known() []uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]uint64(nil), e.keys...)
}

func (e *anthropicEntries) add(keys []uint64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, k := range keys {
		for i, old := range e.keys {
			if old == k {
				e.keys = append(e.keys[:i], e.keys[i+1:]...)
				break
			}
		}
		e.keys = append(e.keys, k)
	}
	if n := len(e.keys) - anthropicEntriesKept; n > 0 {
		e.keys = append([]uint64(nil), e.keys[n:]...)
	}
}

// anthropicNonStreaming parses a plain Anthropic Messages JSON body for
// compatible servers that ignore stream=true.
func (c *Client) anthropicNonStreaming(resp *http.Response) (*Response, error) {
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	var body struct {
		Type       string `json:"type"`
		Model      string `json:"model"`
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type      string          `json:"type"`
			Text      string          `json:"text"`
			ID        string          `json:"id"`
			Name      string          `json:"name"`
			Input     json.RawMessage `json:"input"`
			Thinking  string          `json:"thinking"`
			Signature string          `json:"signature"`
			Data      string          `json:"data"`
		} `json:"content"`
		Usage *struct {
			Input      int `json:"input_tokens"`
			Output     int `json:"output_tokens"`
			CacheRead  int `json:"cache_read_input_tokens"`
			CacheWrite int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
		Error *struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("non-stream response was not JSON: %w", err)
	}
	if body.Error != nil {
		e := streamError(nil, body.Error.Type, body.Error.Message, raw)
		if _, known := streamStatus[body.Error.Type]; !known {
			e.Status = 502
		}
		return nil, e
	}
	out := &Response{Model: body.Model}
	var text, reasoning strings.Builder
	native := make([]map[string]any, 0, len(body.Content))
	for _, b := range body.Content {
		switch b.Type {
		case "text":
			text.WriteString(b.Text)
			if strings.TrimSpace(b.Text) != "" {
				native = append(native, map[string]any{"type": "text", "text": b.Text})
			}
		case "tool_use":
			inRaw := strings.TrimSpace(string(b.Input))
			if inRaw == "" {
				inRaw = "{}"
			}
			args := normalizeArgs(inRaw)
			out.ToolCalls = append(out.ToolCalls, event.ToolCall{ID: b.ID, Name: b.Name, Args: args})
			var input any = json.RawMessage(`{}`)
			if json.Valid([]byte(inRaw)) && strings.HasPrefix(inRaw, "{") {
				input = json.RawMessage(inRaw)
			}
			native = append(native, map[string]any{"type": "tool_use", "id": b.ID, "name": b.Name, "input": input})
		case "thinking":
			reasoning.WriteString(b.Thinking)
			if b.Signature != "" || !claudeModel(c.Endpoint.Model) {
				native = append(native, map[string]any{"type": "thinking", "thinking": b.Thinking, "signature": b.Signature})
			}
		case "redacted_thinking":
			native = append(native, map[string]any{"type": "redacted_thinking", "data": b.Data})
		}
	}
	out.Text = text.String()
	out.Reasoning = reasoning.String()
	if nat, err := json.Marshal(native); err == nil {
		out.Native = nat
	}
	if body.Usage != nil {
		out.Usage.Input = body.Usage.Input + body.Usage.CacheRead + body.Usage.CacheWrite
		out.Usage.Cached = body.Usage.CacheRead
		out.Usage.Output = body.Usage.Output
	}
	switch body.StopReason {
	case "max_tokens":
		out.Stop = "length"
	case "tool_use":
		out.Stop = "tool_calls"
	default:
		if len(out.ToolCalls) > 0 {
			out.Stop = "tool_calls"
		} else {
			out.Stop = "stop"
		}
	}
	return out, nil
}

// anthropicFlavourRejected reports a 400 that is about the thinking
// parameter itself (the model wants the other flavour), as opposed to one
// about a content block in the history that merely mentions thinking.
func anthropicFlavourRejected(body string, budgeted bool) bool {
	if strings.Contains(body, "messages.") || strings.Contains(body, "redacted_thinking") {
		return false
	}
	if budgeted {
		return strings.Contains(body, "thinking.type") || strings.Contains(body, "thinking.budget_tokens")
	}
	return strings.Contains(body, "thinking.type") || strings.Contains(body, "adaptive") || strings.Contains(body, "output_config")
}
