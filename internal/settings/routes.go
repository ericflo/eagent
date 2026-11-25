package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/llm"
	"github.com/ericflo/eagent/internal/projection"
	"github.com/ericflo/eagent/internal/protocol/control"
)

// RouteTestRequest is one model route to try with a single call: Route picks
// its primary or its fallback, and Effort, when given, replaces its reasoning
// effort.
type RouteTestRequest struct {
	Model  config.ModelRoute `json:"model"`
	Effort *string           `json:"effort"`
	Route  string            `json:"route"`
}

type RouteTestResult struct {
	Route      string         `json:"route"`
	BaseURL    string         `json:"base_url"`
	Model      string         `json:"model"`
	Protocol   string         `json:"protocol"`
	Effort     string         `json:"effort"`
	Status     string         `json:"status"`
	MS         int64          `json:"ms"`
	Usage      event.Usage    `json:"usage"`
	CostUSD    float64        `json:"est_cost_usd"`
	Priced     bool           `json:"priced"`
	Sent       map[string]any `json:"sent"`
	Text       string         `json:"text,omitempty"`
	Error      string         `json:"error,omitempty"`
	HTTPStatus int            `json:"http_status,omitempty"`
	At         time.Time      `json:"at"`
}

var routeTests = struct {
	sync.Mutex
	times []time.Time
	sem   chan struct{}
}{sem: make(chan struct{}, 4)}

// testedRoute is the route a test calls, checked like a saved one.
func (s *Service) testedRoute(b RouteTestRequest) (config.ModelRoute, string, error) {
	a, route := b.Model, "primary"
	if b.Route != "" && b.Route != "primary" && b.Route != "fallback" {
		return a, route, &Error{Status: 400, Message: "unknown route"}
	}
	if b.Route == "fallback" {
		if a.Fallback == nil {
			return a, route, &Error{Status: 400, Message: "this route has no fallback"}
		}
		a, route = *a.Fallback, "fallback"
	}
	a.Fallback = nil
	if b.Effort != nil {
		a.ReasoningEffort = *b.Effort
	}
	probe := config.Defaults()
	probe.Model = a
	if err := s.CheckRoutes(probe); err != nil {
		return a, route, &Error{Status: 422, Message: err.Error()}
	}
	for _, p := range probe.Problems() {
		if p.Severity == "error" && strings.HasPrefix(p.Path, "/model/") {
			return a, route, &Error{Status: 422, Message: strings.TrimPrefix(p.Path, "/model/") + ": " + p.Message}
		}
	}
	return a, route, nil
}

// PrepareRoute accepts only a named target from the reviewed saved
// configuration: "model", or "summarizer" (the dedicated summarizer's route).
// It cannot turn an iframe parameter into a URL or credential lookup.
func (s *Service) PrepareRoute(p control.Proposal, grant control.Grant) (RouteTestRequest, error) {
	var b RouteTestRequest
	if _, err := s.ValidateAction(p, grant); err != nil {
		return b, err
	}
	cfg, err := config.LoadBundle(s.Project, s.Preset, s.Bundle)
	if err != nil {
		return b, err
	}
	switch p.Parameters["target"] {
	case "model":
		b.Model = cfg.Model
	case "summarizer":
		b.Model = cfg.Summarizer.Route(cfg.Model)
	default:
		return b, fmt.Errorf("unknown saved route")
	}
	b.Route, _ = p.Parameters["route"].(string)
	if value, ok := p.Parameters["effort"].(string); ok {
		b.Effort = &value
	}
	_, _, err = s.testedRoute(b)
	return b, err
}

// TestRoute is shared by the local editor and remote commands: one attempt,
// four concurrent calls, forty per minute, two minutes and 4096 output tokens.
func (s *Service) TestRoute(parent context.Context, b RouteTestRequest) (RouteTestResult, error) {
	if err := parent.Err(); err != nil {
		return RouteTestResult{}, err
	}
	a, route, err := s.testedRoute(b)
	if err != nil {
		return RouteTestResult{}, err
	}
	routeTests.Lock()
	cutoff := time.Now().Add(-time.Minute)
	kept := routeTests.times[:0]
	for _, at := range routeTests.times {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	routeTests.times = kept
	if len(kept) >= 40 {
		routeTests.Unlock()
		return RouteTestResult{}, &Error{Status: 429, Message: "more than forty route tests in a minute; wait a little"}
	}
	routeTests.times = append(kept, time.Now())
	routeTests.Unlock()
	res := RouteTestResult{Route: route, BaseURL: a.BaseURL, Model: a.Model, Protocol: a.Protocol, Effort: a.ReasoningEffort, At: time.Now(), Sent: routeSent(a)}
	ep, err := a.Endpoint()
	if err != nil {
		res.Status, res.Error = "no_key", err.Error()
		return res, nil
	}
	select {
	case routeTests.sem <- struct{}{}:
	case <-parent.Done():
		return res, parent.Err()
	}
	defer func() { <-routeTests.sem }()
	c := llm.NewClient(ep)
	c.MaxAttempts = 1
	ctx, cancel := context.WithTimeout(parent, 120*time.Second)
	defer cancel()
	start := time.Now()
	resp, err := c.Complete(ctx, llm.Request{System: "Reply with a single tool call.", Messages: []llm.Message{{Role: "user", Text: "Call ping with message=\"pong\"."}}, Tools: []llm.Tool{{Name: "ping", Description: "Ping.", Parameters: json.RawMessage(`{"type":"object","properties":{"message":{"type":"string"}},"required":["message"]}`)}}, MaxTokens: 4096}, nil)
	res.MS = time.Since(start).Milliseconds()
	if err != nil && parent.Err() != nil {
		return res, parent.Err()
	}
	clean := func(text string, max int) string {
		if ep.APIKey != "" {
			text = strings.ReplaceAll(text, ep.APIKey, "[redacted]")
		}
		text = strings.ReplaceAll(text, "\n", " ")
		if len(text) > max {
			text = text[:max] + "…"
		}
		return text
	}
	switch {
	case err != nil && (errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded)):
		res.Status, res.Error = "timeout", "no answer within two minutes"
	case err != nil:
		res.Status, res.Error = "failed", clean(err.Error(), 400)
		var apiError *llm.APIError
		if errors.As(err, &apiError) {
			res.HTTPStatus = apiError.Status
		}
	case len(resp.ToolCalls) == 0:
		res.Status, res.Text, res.Usage = "no_tool_call", clean(resp.Text, 200), resp.Usage
	default:
		res.Status, res.Usage = "tool_call", resp.Usage
	}
	if res.Usage.Input > 0 {
		res.CostUSD, res.Priced = projection.PriceFor(a.BaseURL, a.Model, res.Usage)
	}
	return res, nil
}

func routeSent(a config.ModelRoute) map[string]any {
	e := a.ReasoningEffort
	switch a.Protocol {
	case llm.ProtocolResponses:
		if e == "" || e == "none" {
			return map[string]any{"reasoning": nil, "note": "no reasoning parameter is sent; the model uses its default"}
		}
		return map[string]any{"reasoning": map[string]any{"effort": e}}
	case llm.ProtocolAnthropic:
		if e == "" || e == "none" {
			return map[string]any{"thinking": nil, "note": "no thinking block is requested"}
		}
		return map[string]any{"thinking": map[string]any{"type": "adaptive"}, "output_config": map[string]any{"effort": e}, "note": "older models get a budget instead: low 2048, medium 8192, high 24576"}
	default:
		if e == "" {
			return map[string]any{"note": "no reasoning_effort is sent; the provider default applies"}
		}
		return map[string]any{"reasoning_effort": e}
	}
}
