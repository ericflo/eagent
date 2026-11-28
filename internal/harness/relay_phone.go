package harness

import (
	"context"
	"strconv"
	"strings"
	"unicode/utf8"

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

// loadReceipts rebuilds the receipts and which send forwarded which user
// message from the record, the first time either is needed: a resumed
// session keeps the receipts set before it stopped (every patch carries
// them all), and a confirmation of an earlier send still lands. sendSeq
// is nil until this has run. Loop goroutine.
func (p *phone) loadReceipts(r *Runtime) {
	p.mu.Lock()
	loaded := p.sendSeq != nil
	p.mu.Unlock()
	if loaded {
		return
	}
	sendSeq := map[string]int64{}
	var receipts []relayReceipt
	phoneIDs := map[int64]string{}
	mirrored := map[int64]string{} // terminal and web messages: the thread message showing each
	active := map[string]bool{}
	unsent := map[int64]bool{} // user messages not kept for eagent, with no send so far
	set := func(seq int64, status string) {
		for i := range receipts {
			if receipts[i].seq == seq {
				receipts[i].status = status
				return
			}
		}
		receipts = append(receipts, relayReceipt{seq: seq, phoneID: phoneIDs[seq], status: status})
	}
	for _, ev := range r.st.Events {
		switch ev.Type {
		case event.RelayStart, event.RelayEnd:
			var d struct {
				Name string `json:"name"`
			}
			if ev.Decode(&d) == nil {
				active[d.Name] = ev.Type == event.RelayStart
				if !active[d.Name] {
					delete(active, d.Name)
				}
			}
		case event.PhoneMirror:
			var d event.PhoneMirrorData
			if ev.Decode(&d) == nil && d.Seq > 0 {
				mirrored[d.Seq] = d.MessageID
			}
		case event.UserMessage:
			var d event.UserMessageData
			if ev.Decode(&d) != nil {
				continue
			}
			phoneIDs[ev.Seq] = d.PhoneID
			if d.Held != "" && len(active) > 0 {
				set(ev.Seq, "eagent") // as userMessage marks it
			}
			unsent[ev.Seq] = d.Held == ""
		case event.SessionResume:
			// A resume takes up a message the run before recorded and
			// stopped before sending (closeInterrupted): forwarded, its
			// send is recorded before this event; with none, it was kept
			// for eagent, as userMessage marks one.
			var d event.SessionResumeData
			if ev.Decode(&d) == nil && unsent[d.After] && len(active) > 0 {
				set(d.After, "eagent")
			}
		case event.RelaySend:
			var d event.RelaySendData
			if ev.Decode(&d) != nil || d.From != "user" || d.Seq == 0 {
				continue
			}
			delete(unsent, d.Seq)
			sendSeq[d.ID] = d.Seq
			// From every send of the message, as the live path does.
			set(d.Seq, sendsReceipt(r.st, sendSeq, d.Seq))
		case event.RelayDelivered:
			var d event.RelayDeliveredData
			if ev.Decode(&d) != nil || sendSeq[d.ID] == 0 {
				continue
			}
			set(sendSeq[d.ID], sendsReceipt(r.st, sendSeq, sendSeq[d.ID]))
		}
	}
	if len(receipts) > maxReceipts {
		receipts = receipts[len(receipts)-maxReceipts:]
	}
	for i := range receipts {
		// The copy of a terminal or web message is recorded after the
		// message (once its post returns), so it is matched here.
		if receipts[i].phoneID == "" {
			receipts[i].phoneID = mirrored[receipts[i].seq]
		}
	}
	p.mu.Lock()
	if p.sendSeq == nil {
		p.sendSeq, p.receipts = sendSeq, receipts
	}
	p.mu.Unlock()
}

// setReceipt records a receipt and republishes them. Loop goroutine.
func (p *phone) setReceipt(r *Runtime, seq int64, status string) {
	p.loadReceipts(r)
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
	p.publishReceipts(r)
}

// publishReceipts sends the receipts to the thread, as they stand. Called
// with p.mu held; it lets go of it.
func (p *phone) publishReceipts(r *Runtime) {
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
	case event.RelaySend, event.RelayDelivered:
		p.loadReceipts(r)
	}
	switch ev.Type {
	case event.RelaySend:
		var d event.RelaySendData
		if ev.Decode(&d) != nil || d.From != "user" || d.Seq == 0 {
			return
		}
		p.mu.Lock()
		p.sendSeq[d.ID] = d.Seq
		status := sendsReceipt(r.st, p.sendSeq, d.Seq)
		p.mu.Unlock()
		p.setReceipt(r, d.Seq, status)
	case event.RelayDelivered:
		var d event.RelayDeliveredData
		if ev.Decode(&d) != nil {
			return
		}
		p.mu.Lock()
		seq := p.sendSeq[d.ID]
		status := sendsReceipt(r.st, p.sendSeq, seq)
		p.mu.Unlock()
		if seq == 0 {
			return
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
		// One post. A report too long for FinaleChat to take is clipped at
		// a line's end and says so: the whole of it is in the record.
		if len(body) > phoneReportMax {
			cut := strings.LastIndexByte(body[:phoneReportMax], '\n')
			if cut <= 0 {
				for cut = phoneReportMax; !utf8.RuneStart(body[cut]); cut-- {
				}
			}
			body = strings.TrimRight(body[:cut], "\n") + "\n\n_(clipped; the whole report is in the session's record)_"
		}
		req := finalechat.PostRequest{Body: body, Notify: boolPtr(false),
			Meta: map[string]any{"eagent": "relay", "kind": "relay_report", "relay": d.Name, "agent": agent, "duration_ms": d.DurationMS, "tools": d.Tools, "interrupted": d.Interrupted, "images": images, "seq": ev.Seq, "source_anchor": p.sourceAnchor(ev.Seq)}}
		if r.relayNotifyDone[d.Name] {
			delete(r.relayNotifyDone, d.Name)
			req.Notify, req.Importance = nil, "important"
			r.publishRelayMeta()
		}
		queued := p.enqueue(func(ctx context.Context) {
			// The key and the status are read here, on the worker: until
			// the first call has learned what the server supports, a key
			// made on the loop would be empty, and the post would lose
			// both its idempotency and its patient retries. An agent's
			// post without a status clears the thread's line, so it
			// carries the one the work is in, as a message does.
			req.ClientKey = p.key("rt", strconv.FormatInt(ev.Seq, 10))
			act := p.attach()
			req.Activity = act
			if msg, thread, err := p.client.Post(ctx, p.ref, req); err == nil {
				p.rememberIn(msg, thread)
				p.markSent(act)
			}
		})
		if !queued {
			p.say("finalechat: %s's report (event %d) was not sent to the phone: too much is waiting to be sent; the whole report is in the session's record", agent, ev.Seq)
		}
	}
}

// phoneReportMax bounds the post of an agent's report. FinaleChat refuses
// a message body over 256 KiB and a request over 1 MiB, and JSON can write
// one byte of the body as six ("<" is \u003c), so 128 KiB is taken
// whatever it holds.
const phoneReportMax = 128 << 10

// postRelayScreen shows the agent's terminal in the thread. Any goroutine.
func (p *phone) postRelayScreen(name, screen string) {
	body := "```\n" + strings.TrimRight(redactSecrets(screen), "\n") + "\n```"
	p.enqueue(func(ctx context.Context) {
		// Without a status the post would clear the thread's line, which
		// may be saying the agent needs the user.
		act := p.attach()
		if msg, thread, err := p.client.Post(ctx, p.ref, finalechat.PostRequest{Body: body, Notify: boolPtr(false), Meta: map[string]any{"eagent": "relay", "kind": "relay_screen", "relay": name}, Activity: act}); err == nil {
			p.rememberIn(msg, thread)
			p.markSent(act)
		}
	})
}
