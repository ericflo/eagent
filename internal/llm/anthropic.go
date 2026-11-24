package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

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
	body := map[string]any{
		"model":      c.Endpoint.Model,
		"messages":   anthropicMessages(req, c.Endpoint.BaseURL),
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
			if i == len(req.Tools)-1 {
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
			budget, known := map[string]int{"low": 2048, "medium": 8192, "high": 24576}[e]
			if !known {
				// Never quietly turn an unknown effort into medium.
				return nil, fmt.Errorf("reasoning effort %q is not one this model accepts (none, low, medium, high)", e)
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
			msg := "stream error"
			if frame.Error != nil {
				msg = frame.Error.Type + ": " + frame.Error.Message
			}
			failure = &APIError{Status: 529, Body: msg}
			if frame.Error != nil && frame.Error.Type == "overloaded_error" {
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
			native = append(native, map[string]any{"type": "thinking", "thinking": b.Thinking, "signature": b.Signature})
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

// anthropicMessages renders history with strict user/assistant alternation
// and a cache breakpoint on the last stable message.
func anthropicMessages(req Request, host string) []map[string]any {
	var msgs []map[string]any
	push := func(role string, content []map[string]any) {
		if len(msgs) > 0 && msgs[len(msgs)-1]["role"] == role {
			prev := msgs[len(msgs)-1]["content"].([]map[string]any)
			msgs[len(msgs)-1]["content"] = append(prev, content...)
			return
		}
		msgs = append(msgs, map[string]any{"role": role, "content": content})
	}
	for _, m := range req.Messages {
		switch m.Role {
		case "user":
			text := m.Text
			if text == "" {
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
			if len(m.Native) > 0 && m.NativeProtocol == ProtocolAnthropic && (m.NativeHost == "" || sameHost(m.NativeHost, host)) {
				var native []map[string]any
				if json.Unmarshal(m.Native, &native) == nil && len(native) > 0 {
					content = native
				}
			}
			if content == nil {
				if m.Text != "" {
					content = append(content, map[string]any{"type": "text", "text": m.Text})
				}
				for _, tc := range m.ToolCalls {
					var input any = json.RawMessage(`{}`)
					if obj, err := ArgsObject(tc.Args); err == nil {
						input = obj
					}
					content = append(content, map[string]any{"type": "tool_use", "id": tc.ID, "name": tc.Name, "input": input})
				}
			}
			if len(content) == 0 {
				content = []map[string]any{{"type": "text", "text": "(no output)"}}
			}
			push("assistant", content)
		case "tool":
			var content []map[string]any
			for _, r := range m.Results {
				block := map[string]any{"type": "tool_result", "tool_use_id": r.CallID, "content": r.Output}
				if r.IsError {
					block["is_error"] = true
				}
				content = append(content, block)
			}
			push("user", content)
		}
	}
	// Cache breakpoints on the final message and (when present) the one before
	// it so both normal multi-turn loops (extending msgs[len-1]) and unrecorded
	// checks (replacing msgs[len-1] while keeping msgs[len-2]) hit the cache.
	// Along with system and the last tool, this stays within Anthropic's
	// four-breakpoint limit.
	markCacheable := func(msg map[string]any) {
		blocks, _ := msg["content"].([]map[string]any)
		for i := len(blocks) - 1; i >= 0; i-- {
			b := blocks[i]
			t, _ := b["type"].(string)
			if t == "thinking" || t == "redacted_thinking" {
				continue
			}
			if t == "text" {
				if s, _ := b["text"].(string); s == "" {
					continue
				}
			}
			b["cache_control"] = map[string]any{"type": "ephemeral"}
			return
		}
	}
	if len(msgs) >= 1 {
		markCacheable(msgs[len(msgs)-1])
	}
	if len(msgs) >= 2 {
		markCacheable(msgs[len(msgs)-2])
	}
	return msgs
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
		return nil, &APIError{Status: 502, Body: body.Error.Type + ": " + body.Error.Message}
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
			native = append(native, map[string]any{"type": "thinking", "thinking": b.Thinking, "signature": b.Signature})
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
