package llm

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/ericflo/eagent/internal/event"
)

// anthropic implements the Anthropic Messages API with streaming and prompt
// caching. Thinking blocks are kept in Native and replayed verbatim.
func (c *Client) anthropic(ctx context.Context, req Request, obs *Observer) (*Response, error) {
	maxTokens := c.maxTokens(req)
	body := map[string]any{
		"model":      c.Endpoint.Model,
		"messages":   anthropicMessages(req),
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
		switch req.ToolChoice {
		case "required":
			body["tool_choice"] = map[string]any{"type": "any"}
		case "none":
			body["tool_choice"] = map[string]any{"type": "none"}
		}
	}
	if e := c.Endpoint.ReasoningEffort; e != "" && e != "none" {
		budget := map[string]int{"low": 2048, "medium": 8192, "high": 24576}[e]
		if budget == 0 {
			budget = 8192
		}
		if budget >= maxTokens {
			budget = maxTokens / 2
		}
		if budget >= 1024 {
			body["thinking"] = map[string]any{"type": "enabled", "budget_tokens": budget}
			// Thinking is incompatible with forced tool use.
			if req.ToolChoice == "required" {
				delete(body, "tool_choice")
			}
		}
	}
	resp, err := c.post(ctx, "/messages", body, map[string]string{
		"x-api-key":         c.Endpoint.APIKey,
		"anthropic-version": "2023-06-01",
	})
	if err != nil {
		return nil, err
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
			b := &block{Type: frame.ContentBlock.Type, ID: frame.ContentBlock.ID, Name: frame.ContentBlock.Name}
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
			native = append(native, map[string]any{"type": "text", "text": b.Text})
		case "tool_use":
			raw := strings.TrimSpace(b.partial.String())
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
func anthropicMessages(req Request) []map[string]any {
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
			push("user", []map[string]any{{"type": "text", "text": text}})
		case "assistant":
			var content []map[string]any
			if len(m.Native) > 0 && m.NativeProtocol == ProtocolAnthropic {
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
	// Cache breakpoint on the last block of the final message so the whole
	// history is reusable next turn (the steering message is appended after
	// this by the caller, inside the same final user message).
	if len(msgs) >= 2 {
		prev := msgs[len(msgs)-2]["content"].([]map[string]any)
		if len(prev) > 0 {
			last := prev[len(prev)-1]
			if last["type"] != "thinking" && last["type"] != "redacted_thinking" {
				last["cache_control"] = map[string]any{"type": "ephemeral"}
			}
		}
	}
	return msgs
}
