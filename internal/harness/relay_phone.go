package harness

import (
	"context"
	"strconv"
	"strings"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/finalechat"
)

// What a relay thread on the phone shows beyond the narrator: a receipt
// under each of the user's messages, the agent's own report for each turn,
// and terminal snapshots on request.

// relayReceipt is what became of one user message in a relay thread:
// "sent" (typed into the agent), "delivered" (its transcript shows it),
// "held" (waits for the end of its turn), "eagent" (kept for eagent, not
// forwarded) or "failed".
type relayReceipt struct {
	seq     int64
	phoneID string // the thread message, when it came from the phone
	status  string
}

// maxReceipts bounds the thread meta: only recent messages carry one.
const maxReceipts = 40

// setReceipt records a receipt and republishes them. Loop goroutine.
func (p *phone) setReceipt(r *Runtime, seq int64, status string) {
	phoneID := ""
	for i := len(r.st.Events) - 1; i >= 0; i-- {
		if ev := r.st.Events[i]; ev.Seq == seq {
			var d event.UserMessageData
			if ev.Type == event.UserMessage && ev.Decode(&d) == nil {
				phoneID = d.PhoneID
			}
			break
		}
	}
	p.mu.Lock()
	found := false
	for i := range p.receipts {
		if p.receipts[i].seq == seq {
			p.receipts[i].status = status
			found = true
		}
	}
	if !found {
		p.receipts = append(p.receipts, relayReceipt{seq: seq, phoneID: phoneID, status: status})
		if len(p.receipts) > maxReceipts {
			p.receipts = p.receipts[len(p.receipts)-maxReceipts:]
		}
	}
	p.mu.Unlock()
	p.enqueue(func(ctx context.Context) {
		// On the worker, after the mirror post for a terminal message
		// (queued earlier) has learned that message's id.
		p.mu.Lock()
		out := map[string]any{}
		for _, rc := range p.receipts {
			id := rc.phoneID
			if id == "" {
				id = p.mirrored[rc.seq]
			}
			if id != "" {
				out[id] = rc.status
			}
		}
		p.mu.Unlock()
		if len(out) > 0 {
			_, _ = p.client.Patch(ctx, p.ref, finalechat.PatchRequest{Meta: map[string]any{"relay_receipts": out}})
		}
	})
}

// observeRelay keeps receipts in step with sends and confirmations, and
// posts each turn's report. Loop goroutine.
func (p *phone) observeRelay(r *Runtime, ev event.Event) {
	switch ev.Type {
	case event.RelaySend:
		var d event.RelaySendData
		if ev.Decode(&d) != nil || d.From != "user" || d.Seq == 0 {
			return
		}
		p.mu.Lock()
		if p.sendSeq == nil {
			p.sendSeq = map[string]int64{}
		}
		p.sendSeq[d.ID] = d.Seq
		p.mu.Unlock()
		status := "sent"
		if d.Mode == "after_turn" {
			status = "held"
		}
		p.setReceipt(r, d.Seq, status)
	case event.RelayDelivered:
		var d event.RelayDeliveredData
		if ev.Decode(&d) != nil {
			return
		}
		p.mu.Lock()
		seq := p.sendSeq[d.ID]
		p.mu.Unlock()
		if seq == 0 {
			return
		}
		status := "delivered"
		if !d.OK {
			status = "failed"
		}
		p.setReceipt(r, seq, status)
	case event.RelayTurn:
		var d event.RelayTurnData
		if ev.Decode(&d) != nil {
			return
		}
		// The agent's own words for the turn, verbatim; the app shows them
		// folded under the narrator's summary. Quiet, unless the user asked
		// to hear when this turn ended.
		body := strings.TrimSpace(d.Text)
		if body == "" {
			body = "_(it ended the turn without a message)_"
		}
		agent := d.Name
		if rel := r.st.Relays[d.Name]; rel != nil {
			agent = rel.Agent
		}
		images := d.Images
		if images == nil {
			images = []string{}
		}
		req := finalechat.PostRequest{Body: body, Notify: boolPtr(false), ClientKey: p.key("rt", strconv.FormatInt(ev.Seq, 10)),
			Meta: map[string]any{"eagent": "relay", "kind": "relay_report", "relay": d.Name, "agent": agent, "duration_ms": d.DurationMS, "tools": d.Tools, "interrupted": d.Interrupted, "images": images, "seq": ev.Seq, "source_anchor": p.sourceAnchor(ev.Seq)}}
		if r.relayNotifyDone[d.Name] {
			delete(r.relayNotifyDone, d.Name)
			req.Notify, req.Importance = nil, "important"
			r.publishRelayMeta()
		}
		p.enqueue(func(ctx context.Context) {
			if msg, _, err := p.client.Post(ctx, p.ref, req); err == nil {
				p.remember(msg.ID)
			}
		})
	}
}

// postRelayScreen shows the agent's terminal in the thread. Any goroutine.
func (p *phone) postRelayScreen(name, screen string) {
	body := "```\n" + strings.TrimRight(redactSecrets(screen), "\n") + "\n```"
	p.enqueue(func(ctx context.Context) {
		if msg, _, err := p.client.Post(ctx, p.ref, finalechat.PostRequest{Body: body, Notify: boolPtr(false), Meta: map[string]any{"eagent": "relay", "kind": "relay_screen", "relay": name}}); err == nil {
			p.remember(msg.ID)
		}
	})
}
