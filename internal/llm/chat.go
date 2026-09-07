package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/ericflo/eagent/internal/event"
)

// chat implements OpenAI-style Chat Completions with streaming. This is the
// route for Together AI (GLM, DeepSeek) and any OpenAI-compatible server.
func (c *Client) chat(ctx context.Context, req Request, obs *Observer) (*Response, error) {
	body := map[string]any{
		"model":          c.Endpoint.Model,
		"messages":       chatMessages(req),
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
		"max_tokens":     c.maxTokens(req),
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			tools = append(tools, map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        t.Name,
					"description": t.Description,
					"parameters":  t.Parameters,
				},
			})
		}
		body["tools"] = tools
		switch req.ToolChoice {
		case "required":
			body["tool_choice"] = "required"
		case "none":
			body["tool_choice"] = "none"
		}
	}
	if e := c.Endpoint.ReasoningEffort; e != "" {
		body["reasoning_effort"] = e
	}
	resp, err := c.post(ctx, "/chat/completions", body, map[string]string{
		"Authorization": "Bearer " + c.Endpoint.APIKey,
	})
	if err != nil {
		return nil, err
	}

	type partial struct {
		id, name string
		args     strings.Builder
	}
	var (
		text      strings.Builder
		reasoning strings.Builder
		calls     = map[int]*partial{}
		out       = &Response{}
		finish    string
	)
	err = c.readSSE(ctx, resp, func(ev sseEvent) error {
		data := strings.TrimSpace(string(ev.Data))
		if data == "" || data == "[DONE]" {
			return nil
		}
		var chunk struct {
			Model   string `json:"model"`
			Choices []struct {
				Delta struct {
					Content          *string `json:"content"`
					Reasoning        *string `json:"reasoning"`
					ReasoningContent *string `json:"reasoning_content"`
					ToolCalls        []struct {
						Index    *int   `json:"index"`
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
				PromptDetails    *struct {
					Cached int `json:"cached_tokens"`
				} `json:"prompt_tokens_details"`
				CompletionDetails *struct {
					Reasoning int `json:"reasoning_tokens"`
				} `json:"completion_tokens_details"`
			} `json:"usage"`
			Error *struct {
				Message string `json:"message"`
				Code    any    `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return nil // tolerate keepalives / unknown frames
		}
		if chunk.Error != nil {
			return &APIError{Status: 502, Body: chunk.Error.Message}
		}
		if chunk.Model != "" {
			out.Model = chunk.Model
		}
		if chunk.Usage != nil {
			out.Usage.Input = chunk.Usage.PromptTokens
			out.Usage.Output = chunk.Usage.CompletionTokens
			if chunk.Usage.PromptDetails != nil {
				out.Usage.Cached = chunk.Usage.PromptDetails.Cached
			}
			if chunk.Usage.CompletionDetails != nil {
				out.Usage.Reasoning = chunk.Usage.CompletionDetails.Reasoning
			}
		}
		for _, ch := range chunk.Choices {
			if ch.Delta.Content != nil {
				text.WriteString(*ch.Delta.Content)
				obs.text(*ch.Delta.Content)
			}
			if r := ch.Delta.ReasoningContent; r != nil && *r != "" {
				reasoning.WriteString(*r)
				obs.reasoning(*r)
			} else if r := ch.Delta.Reasoning; r != nil && *r != "" {
				reasoning.WriteString(*r)
				obs.reasoning(*r)
			}
			for _, tc := range ch.Delta.ToolCalls {
				idx := len(calls)
				if tc.Index != nil {
					idx = *tc.Index
				}
				p, ok := calls[idx]
				if !ok {
					p = &partial{}
					calls[idx] = p
				}
				if tc.ID != "" {
					p.id = tc.ID
				}
				if tc.Function.Name != "" {
					p.name += tc.Function.Name
					obs.toolCall(p.name)
				}
				p.args.WriteString(tc.Function.Arguments)
			}
			if ch.FinishReason != nil && *ch.FinishReason != "" {
				finish = *ch.FinishReason
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	idxs := make([]int, 0, len(calls))
	for i := range calls {
		idxs = append(idxs, i)
	}
	sort.Ints(idxs)
	for n, i := range idxs {
		p := calls[i]
		if p.name == "" {
			continue
		}
		id := p.id
		if id == "" {
			id = fmt.Sprintf("call_%d", n+1)
		}
		out.ToolCalls = append(out.ToolCalls, event.ToolCall{ID: id, Name: p.name, Args: normalizeArgs(p.args.String())})
	}
	out.Text = text.String()
	out.Reasoning = reasoning.String()
	if len(out.ToolCalls) == 0 && finish != "length" {
		if parsed, rest := ParseTextToolCalls(out.Text); len(parsed) > 0 {
			out.ToolCalls = parsed
			out.Text = rest
		}
	}
	switch finish {
	case "length":
		out.Stop = "length"
	case "tool_calls", "function_call":
		out.Stop = "tool_calls"
	case "", "stop", "end_turn":
		if len(out.ToolCalls) > 0 {
			out.Stop = "tool_calls"
		} else {
			out.Stop = "stop"
		}
	default:
		out.Stop = finish
	}
	return out, nil
}

// chatMessages renders provider-neutral history as chat-completions messages.
func chatMessages(req Request) []map[string]any {
	var msgs []map[string]any
	if req.System != "" {
		msgs = append(msgs, map[string]any{"role": "system", "content": req.System})
	}
	for _, m := range req.Messages {
		switch m.Role {
		case "user":
			msgs = append(msgs, map[string]any{"role": "user", "content": m.Text})
		case "assistant":
			am := map[string]any{"role": "assistant"}
			if m.Text != "" || len(m.ToolCalls) == 0 {
				am["content"] = m.Text
			} else {
				am["content"] = nil
			}
			if len(m.ToolCalls) > 0 {
				var tcs []map[string]any
				for _, tc := range m.ToolCalls {
					tcs = append(tcs, map[string]any{
						"id":   tc.ID,
						"type": "function",
						"function": map[string]any{
							"name":      tc.Name,
							"arguments": argsString(tc.Args),
						},
					})
				}
				am["tool_calls"] = tcs
			}
			msgs = append(msgs, am)
		case "tool":
			for _, r := range m.Results {
				msgs = append(msgs, map[string]any{
					"role":         "tool",
					"tool_call_id": r.CallID,
					"content":      r.Output,
				})
			}
		}
	}
	return msgs
}

// argsString renders tool-call arguments as the JSON string the wire wants.
func argsString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	// Invalid arguments are stored as a JSON string; unwrap for replay so the
	// model sees what it actually produced.
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}

// normalizeArgs returns raw if it is a JSON object, or a JSON string carrying
// the malformed text so the harness can report it.
func normalizeArgs(raw string) json.RawMessage {
	t := strings.TrimSpace(raw)
	if t == "" {
		return json.RawMessage(`{}`)
	}
	if json.Valid([]byte(t)) && strings.HasPrefix(t, "{") {
		return json.RawMessage(t)
	}
	b, _ := json.Marshal(t)
	return b
}

// ArgsObject decodes tool-call arguments, reporting malformed JSON.
func ArgsObject(raw json.RawMessage) (map[string]any, error) {
	var obj map[string]any
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		return obj, nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if len(s) > 200 {
			s = s[:200] + "..."
		}
		return nil, fmt.Errorf("arguments were not valid JSON (was the output cut off?): %s", s)
	}
	return nil, fmt.Errorf("arguments were not a JSON object")
}
