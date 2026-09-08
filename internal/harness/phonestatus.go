package harness

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/finalechat"
	"github.com/ericflo/eagent/internal/state"
)

// The status line: what the session is doing right now, shown on the phone
// as a typing indicator with a timer. It is derived from the runtime's own
// state after every event, written at most every few seconds, refreshed
// while it stays the same so it never lapses mid-work, and cleared when the
// session ends. It never creates a thread and never notifies anyone.

const (
	statusMinGap      = 2500 * time.Millisecond // between writes
	statusRefresh     = 40 * time.Second        // re-send an unchanged busy status
	statusWaitRefresh = 4 * time.Minute         // re-send an unchanged waiting status
	statusTTL         = 90                      // seconds a busy status lives without a refresh
	statusWaitTTL     = 300                     // seconds a waiting status lives
	meRefresh         = 3 * time.Minute         // re-read the account (remote mode, features)
)

// phoneStats is what the keeper reports as thread meta.
type phoneStats struct {
	Tokens  int
	CostUSD float64
	Priced  bool
}

// noteState recomputes the desired status and the thread stats from the
// runtime and wakes the keeper when the status changed. Loop goroutine only.
func (p *phone) noteState(r *Runtime) {
	want := r.currentActivity()
	stats := r.phoneStats()
	p.mu.Lock()
	changed := want.Text != p.want.Text || want.Kind != p.want.Kind
	p.want = want
	p.stats = stats
	p.mu.Unlock()
	if changed {
		p.kick()
	}
}

func (p *phone) kick() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// has reports whether the server lists a feature.
func (p *phone) has(feature string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.features[feature]
}

// key returns the idempotency key for a post, or "" when the server has no
// idempotency support (an older deployment).
func (p *phone) key(parts ...string) string {
	if !p.has("idempotency") {
		return ""
	}
	return "eagent:" + p.sid + ":" + strings.Join(parts, ":")
}

// attach returns the status to carry on a message or question, so the line
// does not blink off when the narrator speaks while work continues.
func (p *phone) attach() *finalechat.Activity {
	if !p.has("activity") {
		return nil
	}
	p.mu.Lock()
	w := p.want
	p.mu.Unlock()
	if w.Text == "" {
		return nil
	}
	w.Seq = time.Now().UnixNano()
	return &w
}

// markSent records a status the server now shows because a post carried it.
func (p *phone) markSent(a *finalechat.Activity) {
	if a == nil {
		return
	}
	p.mu.Lock()
	p.sent, p.sentAt = *a, time.Now()
	p.mu.Unlock()
}

// currentActivity describes the session's present work in one line. Loop
// goroutine only.
func (r *Runtime) currentActivity() finalechat.Activity {
	if r.ending || r.st.Ended {
		return finalechat.Activity{}
	}
	now := time.Now()
	if procs := r.st.RunningProcs(); len(procs) > 0 {
		text := "Running " + redactSecrets(shortCommand(procs[0].Command))
		if len(procs) > 1 {
			text += fmt.Sprintf(" and %d more", len(procs)-1)
		}
		return finalechat.Activity{Text: oneLine(text, 200), Kind: "tool", TTLSeconds: statusTTL}
	}
	var running []*state.Task
	queued := 0
	for _, t := range r.st.RunningTasks() {
		if t.Status == "running" {
			running = append(running, t)
		} else {
			queued++
		}
	}
	if len(running) > 0 {
		t := running[0]
		text := "Working on " + t.Title
		if step, _ := r.lastStep(t.ID); step != "" {
			text += ": " + step
		}
		if len(running) > 1 {
			text += fmt.Sprintf(" (+%d tasks)", len(running)-1)
		}
		return finalechat.Activity{Text: oneLine(redactSecrets(text), 200), Kind: "working", TTLSeconds: statusTTL}
	}
	if r.orchBusy {
		text := "Thinking about the next step"
		if !r.orchCallAt.IsZero() && now.Sub(r.orchCallAt) > 45*time.Second {
			text = "Still thinking (a long think or a long reply)"
		}
		return finalechat.Activity{Text: text, Kind: "thinking", TTLSeconds: statusTTL}
	}
	if r.narrBusy {
		return finalechat.Activity{Text: "Writing to you", Kind: "typing", TTLSeconds: statusTTL}
	}
	if r.st.Question != nil {
		return finalechat.Activity{Text: "Waiting for your answer", Kind: "waiting", TTLSeconds: statusWaitTTL}
	}
	if queued > 0 {
		return finalechat.Activity{Text: fmt.Sprintf("Waiting for a free worker (%d queued)", queued), Kind: "working", TTLSeconds: statusTTL}
	}
	// Nothing is running, nobody is mid-call, no question is open: the
	// session is waiting on the user, whether it has yielded (Idle) or has
	// simply not started yet (a fresh interactive session before the first
	// message). Idle() alone misses the second case and used to leave a
	// phantom "Working" on the phone.
	if r.opts.Interactive {
		return finalechat.Activity{Text: "Waiting for your next message", Kind: "waiting", TTLSeconds: statusWaitTTL}
	}
	return finalechat.Activity{} // a batch session with nothing left is about to end
}

// phoneStats totals the session's tokens and, when every route is in the
// catalog, its estimated cost. Loop goroutine only.
func (r *Runtime) phoneStats() phoneStats {
	var s phoneStats
	for _, u := range r.st.Totals {
		s.Tokens += u.Input + u.Output
	}
	// Priced per route that served the calls, so a mid-session fallback does
	// not reprice what was already spent elsewhere.
	priced := len(r.st.ByRoute) > 0
	for rk, u := range r.st.ByRoute {
		pr, ok := config.PriceFor(rk.Host, rk.Model)
		if !ok {
			priced = false
			continue
		}
		s.CostUSD += (float64(max(u.Input-u.Cached, 0))*pr.In + float64(u.Cached)*pr.Cached + float64(u.Output)*pr.Out) / 1e6
	}
	s.Priced = priced
	return s
}

// keeper owns the status line and the periodic account re-read: it runs
// beside the outbound worker so a slow post never delays a status, and the
// server's seq ordering settles any race between the two.
func (p *phone) keeper(r *Runtime) {
	defer p.wg.Done()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	lastMe := time.Now()
	var lastMeta phoneStats
	for {
		select {
		case <-p.ctx.Done():
			return
		case <-p.wake:
		case <-timer.C:
		}
		next := statusRefresh
		if p.has("activity") {
			next = p.pushStatus()
		}
		if time.Since(lastMe) >= meRefresh {
			lastMe = time.Now()
			p.refreshMe(r)
			p.mu.Lock()
			st := p.stats
			p.mu.Unlock()
			if st != lastMeta && st.Tokens > 0 {
				lastMeta = st
				meta := map[string]any{"tokens": st.Tokens}
				if st.Priced {
					meta["cost_usd"] = math.Round(st.CostUSD*10000) / 10000
				}
				ctx, cancel := context.WithTimeout(p.ctx, 15*time.Second)
				_, _ = p.client.Patch(ctx, p.ref, finalechat.PatchRequest{Meta: meta})
				cancel()
			}
		}
		if d := meRefresh - time.Since(lastMe); d < next {
			next = d
		}
		if next < 200*time.Millisecond {
			next = 200 * time.Millisecond
		}
		timer.Reset(next)
	}
}

// pushStatus writes the desired status when it changed (never more often
// than statusMinGap) or refreshes an unchanged one before it lapses. It
// returns how long to wait before looking again.
func (p *phone) pushStatus() time.Duration {
	p.mu.Lock()
	want, sent, sentAt := p.want, p.sent, p.sentAt
	p.mu.Unlock()
	now := time.Now()
	changed := want.Text != sent.Text || want.Kind != sent.Kind
	refresh := statusRefresh
	if want.Kind == "waiting" {
		refresh = statusWaitRefresh
	}
	if changed {
		if gap := statusMinGap - now.Sub(sentAt); gap > 0 {
			return gap
		}
	} else {
		if want.Text == "" {
			return statusRefresh
		}
		if due := refresh - now.Sub(sentAt); due > 0 {
			return due
		}
	}
	ctx, cancel := context.WithTimeout(p.ctx, 15*time.Second)
	defer cancel()
	var err error
	if want.Text == "" {
		err = p.client.ClearActivity(ctx, p.ref, now.UnixNano())
	} else {
		want.Seq = now.UnixNano()
		_, err = p.client.SetActivity(ctx, p.ref, want)
	}
	p.mu.Lock()
	p.sentAt = now
	if err == nil {
		p.sent = want
	}
	p.mu.Unlock()
	if err != nil {
		// Best effort: a 404 means the thread is not there yet, anything
		// else is retried after a full gap. Nothing is logged; the status is
		// decoration, not the record.
		var e *finalechat.Error
		if errors.As(err, &e) && e.Status == 404 {
			return statusRefresh
		}
		return 2 * statusMinGap
	}
	return refresh
}

// refreshMe re-reads the account so a remote-mode switch flipped mid-session
// is honoured, and picks up the feature list. Records a new phone.thread
// event when remote mode changed, so the log and the UI see it.
func (p *phone) refreshMe(r *Runtime) {
	ctx, cancel := context.WithTimeout(p.ctx, 15*time.Second)
	defer cancel()
	me, err := p.client.Me(ctx)
	if err != nil {
		return
	}
	remote := me.User.Settings.RemoteMode
	p.mu.Lock()
	was := p.remote
	p.remote = remote
	p.features = featureSet(me.Features)
	p.mu.Unlock()
	if was == remote {
		return
	}
	r.post(func() {
		if r.phone != p || r.st.Phone == nil || r.ending {
			return
		}
		d := *r.st.Phone
		d.RemoteMode = remote
		r.append(event.New(event.PhoneThread, event.ActorHarness, d))
	})
}

func featureSet(list []string) map[string]bool {
	out := map[string]bool{}
	for _, f := range list {
		out[f] = true
	}
	return out
}

// dismissedAnswer is what the orchestrator hears when the user declines a
// question from the phone: the user acted, so it reads as their reply.
const dismissedAnswer = "I dismissed this question from my phone without answering. Use your best judgement (the cautious option if anything is at stake) and tell me what you chose."

// ---- redaction -----------------------------------------------------------------------

var secretPatterns = []struct {
	re   *regexp.Regexp
	repl string
}{
	{regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]+`), "bearer …"},
	{regexp.MustCompile(`(?i)\b([A-Za-z0-9_-]*(?:key|token|secret|password|passwd|pwd|credential)[A-Za-z0-9_-]*)(\s*[=:]\s*)("[^"]*"|'[^']*'|\S+)`), "$1$2…"},
	{regexp.MustCompile(`(?i)(--?[a-z0-9-]*(?:key|token|secret|password|passwd|pwd))(\s+)("[^"]*"|'[^']*'|\S+)`), "$1$2…"},
	{regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^/\s@]+@`), "$1…@"},
	{regexp.MustCompile(`\b(?:fc|sk|ghp|gho|ghu|ghs|glpat|hf|xox[abps])[-_][A-Za-z0-9_-]{12,}\b`), "…"},
}

// redactSecrets blanks the values of anything that looks like a credential
// in a command line or step description: bearer tokens, key=value and
// --flag value pairs whose name says key, token, secret or password, user
// info in URLs, and common token shapes.
func redactSecrets(s string) string {
	for _, p := range secretPatterns {
		s = p.re.ReplaceAllString(s, p.repl)
	}
	return s
}

// oneLine collapses whitespace and caps the text at n runes.
func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
