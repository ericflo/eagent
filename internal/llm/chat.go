package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/ericflo/eagent/internal/event"
)

// nonce makes synthetic ids unique across responses.
func nonce() string { return fmt.Sprintf("%x", time.Now().UnixNano()%0xFFFFFFF) }

// chatNative holds provider-specific assistant reasoning for openai-chat
// replay so prefix caches survive across turns on servers whose chat template
// renders thinking in history (Loom, vLLM, SGLang, llama.cpp, DeepSeek).
type chatNative struct {
	ReasoningContent string `json:"reasoning_content,omitempty"`
	Reasoning        string `json:"reasoning,omitempty"`
}

// chat implements OpenAI-style Chat Completions with streaming. This is the
// route for Together AI (GLM, DeepSeek) and any OpenAI-compatible server.
func (c *Client) chat(ctx context.Context, req Request, obs *Observer) (*Response, error) {
	return c.chatTry(ctx, req, obs, 2)
}

func (c *Client) chatTry(ctx context.Context, req Request, obs *Observer, fallbacks int) (*Response, error) {
	replayReasoning := c.Endpoint.ReplayReasoning && !c.chatNoReplayReasoning.Load()
	msgs, replayedReasoning := chatMessages(req, replayReasoning, c.Endpoint.BaseURL)
	body := map[string]any{
		"model":          c.Endpoint.Model,
		"messages":       msgs,
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
	}
	if e := c.Endpoint.ReasoningEffort; e != "" {
		body["reasoning_effort"] = e
	}
	sentCacheKey := false
	if req.CacheKey != "" && !c.chatNoCacheKey.Load() {
		body["prompt_cache_key"] = req.CacheKey
		sentCacheKey = true
	}
	var headers map[string]string
	if c.Endpoint.APIKey != "" {
		headers = map[string]string{"Authorization": "Bearer " + c.Endpoint.APIKey}
	}
	resp, err := c.post(ctx, "/chat/completions", body, headers)
	if err != nil {
		var ae *APIError
		if fallbacks > 0 && errors.As(err, &ae) && (ae.Status == 400 || ae.Status == 422) {
			if sentCacheKey && chatFieldRejected(ae.Body, "prompt_cache_key") {
				c.chatNoCacheKey.Store(true)
				return c.chatTry(ctx, req, obs, fallbacks-1)
			}
			if replayedReasoning && (chatFieldRejected(ae.Body, "reasoning_content") || chatFieldRejected(ae.Body, "reasoning")) {
				c.chatNoReplayReasoning.Store(true)
				return c.chatTry(ctx, req, obs, fallbacks-1)
			}
			// Generic unknown-field error (e.g. strict schema validation):
			// shed prompt_cache_key first, then reasoning replay.
			if chatGenericExtraFieldError(ae.Body) {
				if sentCacheKey {
					c.chatNoCacheKey.Store(true)
					return c.chatTry(ctx, req, obs, fallbacks-1)
				}
				if replayedReasoning {
					c.chatNoReplayReasoning.Store(true)
					return c.chatTry(ctx, req, obs, fallbacks-1)
				}
			}
		}
		return nil, err
	}
	if ct := resp.Header.Get("Content-Type"); strings.Contains(ct, "application/json") {
		// The server ignored stream=true (some OpenAI-compatible servers do).
		return c.chatNonStreaming(resp)
	}

	type partial struct {
		id, name string
		args     strings.Builder
	}
	var (
		text           strings.Builder
		reasoning      strings.Builder
		reasoningField string
		calls          = map[int]*partial{}
		out            = &Response{}
		finish         string
		lastIdx        int // for deltas that carry no index: continue the last call unless a new one is announced
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
			Error json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return nil // tolerate keepalives / unknown frames
		}
		if len(chunk.Error) > 0 && string(chunk.Error) != "null" {
			var e struct {
				Message string `json:"message"`
				Code    any    `json:"code"`
				Type    string `json:"type"`
			}
			_ = json.Unmarshal(chunk.Error, &e)
			return streamError(e.Code, e.Type, e.Message, chunk.Error)
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
				reasoningField = "reasoning_content"
				obs.reasoning(*r)
			} else if r := ch.Delta.Reasoning; r != nil && *r != "" {
				reasoning.WriteString(*r)
				if reasoningField == "" {
					reasoningField = "reasoning"
				}
				obs.reasoning(*r)
			}
			for _, tc := range ch.Delta.ToolCalls {
				idx := lastIdx
				switch {
				case tc.Index != nil:
					idx = *tc.Index
				case len(calls) == 0:
					idx = 0
				default:
					// No index: a continuation of the last call, unless this
					// delta announces a different one (a new id, or a new
					// name when the last one is already named).
					if prev := calls[lastIdx]; prev == nil || (tc.ID != "" && prev.id != "" && tc.ID != prev.id) || (tc.ID == "" && tc.Function.Name != "" && prev.name != "") {
						idx = len(calls)
					}
				}
				lastIdx = idx
				p, ok := calls[idx]
				if !ok {
					p = &partial{}
					calls[idx] = p
				}
				if tc.ID != "" {
					p.id = tc.ID
				}
				if tc.Function.Name != "" && !strings.HasSuffix(p.name, tc.Function.Name) {
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
	if finish == "" {
		// The connection ended before the provider signalled completion; what
		// we have is a fragment, not a response.
		return nil, ErrTruncatedStream
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
			id = fmt.Sprintf("call_%s_%d", nonce(), n+1)
		}
		out.ToolCalls = append(out.ToolCalls, event.ToolCall{ID: id, Name: p.name, Args: normalizeArgs(p.args.String())})
	}
	out.Text = text.String()
	out.Reasoning = reasoning.String()
	if out.Reasoning != "" {
		out.Native = encodeChatNative(reasoningField, out.Reasoning)
	}
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
// It also reports whether any assistant message carried replayed reasoning.
func chatMessages(req Request, replayReasoning bool, host string) ([]map[string]any, bool) {
	var msgs []map[string]any
	replayedReasoning := false
	if req.System != "" {
		msgs = append(msgs, map[string]any{"role": "system", "content": req.System})
	}
	for _, m := range req.Messages {
		switch m.Role {
		case "user":
			if len(m.Images) == 0 {
				msgs = append(msgs, map[string]any{"role": "user", "content": m.Text})
				continue
			}
			parts := []map[string]any{{"type": "text", "text": m.Text}}
			for _, im := range m.Images {
				if u := im.dataURL(); u != "" {
					parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": u}})
				}
			}
			msgs = append(msgs, map[string]any{"role": "user", "content": parts})
		case "assistant":
			am := map[string]any{"role": "assistant"}
			if m.Text != "" || len(m.ToolCalls) == 0 {
				am["content"] = m.Text
			} else {
				am["content"] = nil
			}
			if replayReasoning && len(m.Native) > 0 && m.NativeProtocol == ProtocolChat && (m.NativeHost == "" || sameHost(m.NativeHost, host)) {
				var cn chatNative
				if json.Unmarshal(m.Native, &cn) == nil {
					if cn.ReasoningContent != "" {
						am["reasoning_content"] = cn.ReasoningContent
						replayedReasoning = true
					} else if cn.Reasoning != "" {
						am["reasoning"] = cn.Reasoning
						replayedReasoning = true
					}
				}
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
	return msgs, replayedReasoning
}

func encodeChatNative(field, reasoning string) json.RawMessage {
	if reasoning == "" {
		return nil
	}
	var cn chatNative
	if field == "reasoning" {
		cn.Reasoning = reasoning
	} else {
		cn.ReasoningContent = reasoning
	}
	b, err := json.Marshal(cn)
	if err != nil {
		return nil
	}
	return b
}

func chatFieldRejected(body, field string) bool {
	b := strings.ToLower(body)
	return strings.Contains(b, field)
}

func chatGenericExtraFieldError(body string) bool {
	b := strings.ToLower(body)
	for _, k := range []string{"unknown field", "unrecognized", "extra_forbidden", "extra inputs are not permitted", "additional properties", "unexpected field", "unsupported parameter"} {
		if strings.Contains(b, k) {
			return true
		}
	}
	return false
}

// argsString renders tool-call arguments as the JSON string the wire wants.
// Malformed arguments (stored as a JSON string) are wrapped in a valid object
// so that one bad turn can never make every later request fail validation.
func argsString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		wrapped, _ := json.Marshal(map[string]string{"_malformed": s})
		return string(wrapped)
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
		RepairLeakedArgs(obj)
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

// chatNonStreaming parses a plain chat-completions JSON body.
func (c *Client) chatNonStreaming(resp *http.Response) (*Response, error) {
	defer resp.Body.Close()
	var body struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content          *string `json:"content"`
				ReasoningContent string  `json:"reasoning_content"`
				Reasoning        string  `json:"reasoning"`
				ToolCalls        []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
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
			Type    string `json:"type"`
		} `json:"error"`
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("non-stream response was not JSON: %w", err)
	}
	if body.Error != nil {
		return nil, streamError(body.Error.Code, body.Error.Type, body.Error.Message, raw)
	}
	if len(body.Choices) == 0 {
		return nil, ErrTruncatedStream
	}
	ch := body.Choices[0]
	out := &Response{Model: body.Model}
	if ch.Message.Content != nil {
		out.Text = *ch.Message.Content
	}
	if ch.Message.ReasoningContent != "" {
		out.Reasoning = ch.Message.ReasoningContent
		out.Native = encodeChatNative("reasoning_content", out.Reasoning)
	} else if ch.Message.Reasoning != "" {
		out.Reasoning = ch.Message.Reasoning
		out.Native = encodeChatNative("reasoning", out.Reasoning)
	}
	for n, tc := range ch.Message.ToolCalls {
		id := tc.ID
		if id == "" {
			id = fmt.Sprintf("call_%s_%d", nonce(), n+1)
		}
		out.ToolCalls = append(out.ToolCalls, event.ToolCall{ID: id, Name: tc.Function.Name, Args: normalizeArgs(tc.Function.Arguments)})
	}
	if len(out.ToolCalls) == 0 && ch.FinishReason != "length" {
		if parsed, rest := ParseTextToolCalls(out.Text); len(parsed) > 0 {
			out.ToolCalls = parsed
			out.Text = rest
		}
	}
	if body.Usage != nil {
		out.Usage.Input, out.Usage.Output = body.Usage.PromptTokens, body.Usage.CompletionTokens
		if body.Usage.PromptDetails != nil {
			out.Usage.Cached = body.Usage.PromptDetails.Cached
		}
		if body.Usage.CompletionDetails != nil {
			out.Usage.Reasoning = body.Usage.CompletionDetails.Reasoning
		}
	}
	switch ch.FinishReason {
	case "length":
		out.Stop = "length"
	default:
		if len(out.ToolCalls) > 0 {
			out.Stop = "tool_calls"
		} else {
			out.Stop = "stop"
		}
	}
	return out, nil
}
