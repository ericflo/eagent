// Package projection shares the deterministic session view between the local
// browser, archive exporter and Go WebAssembly replay. Live process health is
// overlaid only by the local server.
package projection

import (
	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/state"
	"strings"
	"time"
)

type Metadata struct {
	ID                string
	Started, Modified time.Time
	Subsessions       int
	Size              int64
	Pricing           *PricingSnapshot `json:"pricing,omitempty"`
}

// PricingSnapshot freezes the exporter's rate catalog, including unknown
// routes. These are estimates at capture time, not historical invoice rates.
type PricingSnapshot struct {
	Basis string                   `json:"basis"`
	Rates map[string]*config.Price `json:"rates"`
}

func pricingKey(host, model string) string { return strings.TrimRight(host, "/") + "\x00" + model }

func CapturePricing(st *state.State) *PricingSnapshot {
	out := &PricingSnapshot{Basis: "catalog_at_capture", Rates: map[string]*config.Price{}}
	for route := range st.ByRoute {
		key := pricingKey(route.Host, route.Model)
		if price, ok := config.PriceFor(route.Host, route.Model); ok {
			out.Rates[key] = &price
		} else {
			out.Rates[key] = nil
		}
	}
	return out
}

// SessionSummary is what the list and detail endpoints return.
type SessionSummary struct {
	ID           string            `json:"id"`
	Tokens       int               `json:"tokens"`   // input tokens across all actors
	CostUSD      float64           `json:"cost_usd"` // estimate at list prices; 0 when a model is unknown
	Priced       bool              `json:"priced"`
	PriceBasis   string            `json:"price_basis"`
	Duration     float64           `json:"duration_s"`
	Started      time.Time         `json:"started"`
	Modified     time.Time         `json:"modified"`
	Status       string            `json:"status"` // running | idle | done | awaiting-input | interrupted | quit | error
	Alive        bool              `json:"alive"`
	Hosted       bool              `json:"hosted"` // running inside this server
	FirstMessage string            `json:"first_message"`
	Models       map[string]string `json:"models"`
	Config       string            `json:"config,omitempty"`
	Subsessions  int               `json:"subsessions"`
	Size         int64             `json:"size"`
	Events       int               `json:"events"`
	Cwd          string            `json:"cwd,omitempty"`
}

// SessionDetail adds the derived runtime picture.
type SessionDetail struct {
	SessionSummary
	Tasks       []TaskView      `json:"tasks"`
	Procs       []ProcView      `json:"procs"`
	Schedules   []ScheduleView  `json:"schedules"`
	Question    *state.Question `json:"question,omitempty"`
	Idle        bool            `json:"idle"`
	Done        bool            `json:"done"`
	LastReason  string          `json:"last_reason,omitempty"`
	Usage       []UsageView     `json:"usage"`
	Context     int             `json:"context_tokens"`
	Files       []string        `json:"files"`
	Live        any             `json:"live,omitempty"`
	LastSeq     int64           `json:"last_seq"`
	Interactive bool            `json:"interactive"`
	// Phone is set when the session is mirrored to the user's phone.
	Phone *event.PhoneThreadData `json:"phone,omitempty"`
}

type TaskView struct {
	ID      string     `json:"id"`
	Title   string     `json:"title"`
	Kind    string     `json:"kind"`
	Status  string     `json:"status"`
	Summary string     `json:"summary"`
	Turns   int        `json:"turns"`
	Created time.Time  `json:"created"`
	Ended   *time.Time `json:"ended,omitempty"` // nil while the task is running
	Usage   UsageView  `json:"usage"`
}

type ProcView struct {
	Handle   string    `json:"handle"`
	Actor    string    `json:"actor"`
	Task     string    `json:"task,omitempty"`
	Command  string    `json:"command"`
	Status   string    `json:"status"`
	ExitCode int       `json:"exit_code"`
	Started  time.Time `json:"started"`
}

type ScheduleView struct {
	ID    string    `json:"id"`
	Kind  string    `json:"kind"`
	Spec  string    `json:"spec"`
	Note  string    `json:"note"`
	Next  time.Time `json:"next"`
	Fires int       `json:"fires"`
}

type UsageView struct {
	Actor      string  `json:"actor"`
	Model      string  `json:"model,omitempty"`
	CostUSD    float64 `json:"cost_usd"`
	Priced     bool    `json:"priced"`
	Calls      int     `json:"calls"`
	Input      int     `json:"input"`
	Output     int     `json:"output"`
	Cached     int     `json:"cached"`
	Reasoning  int     `json:"reasoning"`
	CacheRatio float64 `json:"cache_ratio"`
	P50MS      int64   `json:"p50_ms,omitempty"`
	P95MS      int64   `json:"p95_ms,omitempty"`
}

func usageView(actor string, calls int, u event.Usage) UsageView {
	v := UsageView{Actor: actor, Calls: calls, Input: u.Input, Output: u.Output, Cached: u.Cached, Reasoning: u.Reasoning}
	if u.Input > 0 {
		v.CacheRatio = float64(u.Cached) / float64(u.Input)
	}
	return v
}

func Summary(info Metadata, st *state.State) SessionSummary {
	evs := st.Events
	sum := SessionSummary{
		ID: info.ID, Started: info.Started, Modified: info.Modified, Subsessions: info.Subsessions, Size: info.Size,
		Models: st.Models, Cwd: st.Cwd, Events: len(evs),
	}
	if len(evs) > 0 {
		var d event.SessionStartData
		_ = evs[0].Decode(&d)
		sum.Config = d.Config
	}
	for _, ev := range evs {
		if ev.Type == event.UserMessage {
			var d event.UserMessageData
			_ = ev.Decode(&d)
			sum.FirstMessage = d.Text
			break
		}
	}
	for _, a := range []string{event.ActorOrchestrator, event.ActorTask, event.ActorNarrator} {
		sum.Tokens += st.Totals[a].Input
	}
	sum.CostUSD, sum.Priced = costOfRoutesWithPrices(st, "", info.Pricing)
	sum.PriceBasis = "viewer_catalog"
	if info.Pricing != nil {
		sum.PriceBasis = info.Pricing.Basis
	}
	if !st.Started.IsZero() {
		end := info.Modified
		if st.Ended {
			if last := st.Events[len(st.Events)-1]; !last.Time.IsZero() {
				end = last.Time
			}
		}
		sum.Duration = end.Sub(st.Started).Seconds()
	}
	switch {
	case !st.Ended && st.Idle() && st.Question != nil:
		sum.Status = "awaiting-input"
	case !st.Ended && st.Idle():
		sum.Status = "idle"
	case !st.Ended:
		sum.Status = "running"
	case st.Ended:
		sum.Status = st.EndReason
	default:
		sum.Status = "interrupted"
	}
	return sum
}

func Detail(info Metadata, st *state.State) *SessionDetail {
	sum := Summary(info, st)
	d := &SessionDetail{SessionSummary: sum, Idle: st.Idle(), LastSeq: st.LastSeq(), Context: st.ContextTokens(event.ActorOrchestrator), Interactive: st.Interactive, Phone: st.Phone}
	if st.LastYield != nil {
		d.Done = st.LastYield.Done
		d.LastReason = st.LastYield.Reason
	}
	d.Question = st.Question
	for _, id := range st.TaskOrder {
		t := st.Tasks[id]
		var ended *time.Time
		if !t.Ended.IsZero() {
			e := t.Ended
			ended = &e
		}
		d.Tasks = append(d.Tasks, TaskView{ID: t.ID, Title: t.Title, Kind: t.Kind, Status: t.Status, Summary: t.Summary, Turns: t.Turns, Created: t.Created, Ended: ended, Usage: usageView(event.ActorTask, t.Turns, t.Usage)})
	}
	for _, h := range st.ProcOrder {
		p := st.Procs[h]
		d.Procs = append(d.Procs, ProcView{Handle: p.Handle, Actor: p.Actor, Task: p.Task, Command: p.Command, Status: p.Status, ExitCode: p.ExitCode, Started: p.Started})
	}
	for _, sc := range st.ActiveSchedules() {
		d.Schedules = append(d.Schedules, ScheduleView{ID: sc.ID, Kind: sc.Kind, Spec: sc.Spec, Note: sc.Note, Next: sc.Next, Fires: sc.Fires})
	}
	for _, a := range []string{event.ActorOrchestrator, event.ActorTask, event.ActorNarrator} {
		u := usageView(a, st.Calls[a], st.Totals[a])
		u.P50MS, u.P95MS = st.Percentile(a, 50), st.Percentile(a, 95)
		u.Model = st.Models[a]
		u.CostUSD, u.Priced = costOfRoutesWithPrices(st, a, info.Pricing)
		d.Usage = append(d.Usage, u)
	}
	for _, ss := range st.Subsessions {
		d.Files = append(d.Files, ss.File)
	}
	if d.Tasks == nil {
		d.Tasks = []TaskView{}
	}
	if d.Procs == nil {
		d.Procs = []ProcView{}
	}
	if d.Schedules == nil {
		d.Schedules = []ScheduleView{}
	}
	return d
}

// priceFor prices a call at list rates from the catalog; ok is false for a
// model the catalog does not know.
func PriceFor(baseURL, model string, u event.Usage) (float64, bool) {
	p, ok := config.PriceFor(baseURL, model)
	if !ok {
		return 0, false
	}
	return pricedUsage(p, u), true
}

func pricedUsage(p config.Price, u event.Usage) float64 {
	uncached := u.Input - u.Cached
	if uncached < 0 {
		uncached = 0
	}
	return (float64(uncached)*p.In + float64(u.Cached)*p.Cached + float64(u.Output)*p.Out) / 1e6
}

// estimateCost sums per-actor estimates; Priced is false if any actor's
// model is unknown so the UI can say so.
func estimateCost(st *state.State) (float64, bool) {
	return costOfRoutes(st, "")
}

func EstimateCost(st *state.State) (float64, bool) { return estimateCost(st) }

// costOfRoutes prices every route that served a call (for one actor, or all
// when actor is ""), so a mid-session fallback does not reprice tokens
// already spent at the previous provider.
func costOfRoutes(st *state.State, actor string) (float64, bool) {
	return costOfRoutesWithPrices(st, actor, nil)
}

func costOfRoutesWithPrices(st *state.State, actor string, pricing *PricingSnapshot) (float64, bool) {
	total := 0.0
	priced := true
	any := false
	for rk, u := range st.ByRoute {
		if actor != "" && rk.Actor != actor {
			continue
		}
		any = true
		var c float64
		var ok bool
		if pricing == nil {
			c, ok = PriceFor(rk.Host, rk.Model, u)
		} else if p := pricing.Rates[pricingKey(rk.Host, rk.Model)]; p != nil {
			c, ok = pricedUsage(*p, u), true
		}
		if !ok {
			priced = false
			continue
		}
		total += c
	}
	return total, priced && any
}
