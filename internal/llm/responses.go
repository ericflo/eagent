package llm

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/ericflo/eagent/internal/event"
)

// responses implements the OpenAI Responses API with streaming. Reasoning
// items are requested with encrypted content and replayed verbatim on later
// turns so the model keeps its chain of thought across tool calls without the
// provider storing anything.
func (c *Client) responses(ctx context.Context, req Request, obs *Observer) (*Response, error) {
	body := map[string]any{
		"model":             c.Endpoint.Model,
		"input":             responsesInput(req),
		"stream":            true,
		"store":             false,
		"max_output_tokens": c.maxTokens(req),
		"include":           []string{"reasoning.encrypted_content"},
	}
	if req.System != "" {
		body["instructions"] = req.System
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			tools = append(tools, map[string]any{
				"type":        "function",
				"name":        t.Name,
				"description": t.Description,
				"parameters":  t.Parameters,
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
	if e := c.Endpoint.ReasoningEffort; e != "" && e != "none" {
		body["reasoning"] = map[string]any{"effort": e, "summary": "auto"}
	}
	if req.CacheKey != "" {
		body["prompt_cache_key"] = req.CacheKey
	}
	resp, err := c.post(ctx, "/responses", body, map[string]string{
		"Authorization": "Bearer " + c.Endpoint.APIKey,
	})
	if err != nil {
		return nil, err
	}

	out := &Response{}
	var items []json.RawMessage
	var final json.RawMessage
	var failure *APIError
	incomplete := ""
	err = c.readSSE(ctx, resp, func(ev sseEvent) error {
		var frame struct {
			Type     string          `json:"type"`
			Delta    string          `json:"delta"`
			Item     json.RawMessage `json:"item"`
			Response json.RawMessage `json:"response"`
			Error    *struct {
				Message string `json:"message"`
				Code    any    `json:"code"`
			} `json:"error"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(ev.Data, &frame); err != nil {
			return nil
		}
		switch frame.Type {
		case "response.output_text.delta":
			obs.text(frame.Delta)
		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			obs.reasoning(frame.Delta)
		case "response.output_item.added":
			var it struct {
				Type string `json:"type"`
				Name string `json:"name"`
			}
			if json.Unmarshal(frame.Item, &it) == nil && it.Type == "function_call" {
				obs.toolCall(it.Name)
			}
		case "response.output_item.done":
			items = append(items, frame.Item)
		case "response.completed":
			final = frame.Response
		case "response.incomplete":
			final = frame.Response
			incomplete = "incomplete"
		case "response.failed":
			final = frame.Response
			msg := "response failed"
			var r struct {
				Error *struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if json.Unmarshal(frame.Response, &r) == nil && r.Error != nil {
				msg = r.Error.Message
			}
			failure = &APIError{Status: 502, Body: msg}
		case "error":
			msg := frame.Message
			if frame.Error != nil {
				msg = frame.Error.Message
			}
			failure = &APIError{Status: 502, Body: msg}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if failure != nil {
		return nil, failure
	}

	// Prefer the authoritative output list from the final response.
	var fin struct {
		Model  string `json:"model"`
		Status string `json:"status"`
		Output []json.RawMessage `json:"output"`
		Usage  *struct {
			Input  int `json:"input_tokens"`
			Output int `json:"output_tokens"`
			InDet  *struct {
				Cached int `json:"cached_tokens"`
			} `json:"input_tokens_details"`
			OutDet *struct {
				Reasoning int `json:"reasoning_tokens"`
			} `json:"output_tokens_details"`
		} `json:"usage"`
		Incomplete *struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
	}
	if len(final) > 0 && json.Unmarshal(final, &fin) == nil {
		if len(fin.Output) > 0 {
			items = fin.Output
		}
		out.Model = fin.Model
		if fin.Usage != nil {
			out.Usage.Input = fin.Usage.Input
			out.Usage.Output = fin.Usage.Output
			if fin.Usage.InDet != nil {
				out.Usage.Cached = fin.Usage.InDet.Cached
			}
			if fin.Usage.OutDet != nil {
				out.Usage.Reasoning = fin.Usage.OutDet.Reasoning
			}
		}
		if fin.Incomplete != nil && strings.Contains(fin.Incomplete.Reason, "max_output_tokens") {
			incomplete = "length"
		}
	}

	var text, reasoning strings.Builder
	cleaned := make([]json.RawMessage, 0, len(items))
	for _, raw := range items {
		var it struct {
			Type    string `json:"type"`
			CallID  string `json:"call_id"`
			Name    string `json:"name"`
			Args    string `json:"arguments"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			Summary []struct {
				Text string `json:"text"`
			} `json:"summary"`
		}
		if err := json.Unmarshal(raw, &it); err != nil {
			continue
		}
		switch it.Type {
		case "message":
			for _, ct := range it.Content {
				if ct.Type == "output_text" || ct.Type == "text" {
					text.WriteString(ct.Text)
				}
			}
		case "function_call":
			out.ToolCalls = append(out.ToolCalls, event.ToolCall{ID: it.CallID, Name: it.Name, Args: normalizeArgs(it.Args)})
		case "reasoning":
			for _, s := range it.Summary {
				if reasoning.Len() > 0 {
					reasoning.WriteString("\n")
				}
				reasoning.WriteString(s.Text)
			}
		}
		cleaned = append(cleaned, stripStatus(raw))
	}
	out.Text = text.String()
	out.Reasoning = reasoning.String()
	if nat, err := json.Marshal(cleaned); err == nil {
		out.Native = nat
	}
	switch {
	case incomplete == "length":
		out.Stop = "length"
	case len(out.ToolCalls) > 0:
		out.Stop = "tool_calls"
	case incomplete != "":
		out.Stop = incomplete
	default:
		out.Stop = "stop"
	}
	return out, nil
}

// stripStatus removes the transient "status" field from an output item so
// it can be sent back as input.
func stripStatus(raw json.RawMessage) json.RawMessage {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return raw
	}
	if _, ok := m["status"]; !ok {
		return raw
	}
	delete(m, "status")
	b, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return b
}

// responsesInput renders history as Responses API input items.
func responsesInput(req Request) []any {
	var items []any
	for _, m := range req.Messages {
		switch m.Role {
		case "user":
			items = append(items, map[string]any{"role": "user", "content": m.Text})
		case "assistant":
			if len(m.Native) > 0 && m.NativeProtocol == ProtocolResponses {
				var native []json.RawMessage
				if json.Unmarshal(m.Native, &native) == nil && len(native) > 0 {
					for _, it := range native {
						items = append(items, it)
					}
					continue
				}
			}
			if m.Text != "" {
				items = append(items, map[string]any{"role": "assistant", "content": m.Text})
			}
			for _, tc := range m.ToolCalls {
				items = append(items, map[string]any{
					"type":      "function_call",
					"call_id":   tc.ID,
					"name":      tc.Name,
					"arguments": argsString(tc.Args),
				})
			}
		case "tool":
			for _, r := range m.Results {
				items = append(items, map[string]any{
					"type":    "function_call_output",
					"call_id": r.CallID,
					"output":  r.Output,
				})
			}
		}
	}
	return items
}
