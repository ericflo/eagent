package harness

import (
	"context"
	"fmt"
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
		case event.RelaySend:
			var d event.RelaySendData
			if ev.Decode(&d) != nil || d.From != "user" || d.Seq == 0 {
				continue
			}
			sendSeq[d.ID] = d.Seq
			status := "sent"
			if d.Mode == "after_turn" {
				status = "held"
			}
			set(d.Seq, status)
		case event.RelayDelivered:
			var d event.RelayDeliveredData
			if ev.Decode(&d) != nil || sendSeq[d.ID] == 0 {
				continue
			}
			status := "delivered"
			if !d.OK {
				status = "failed"
			}
			set(sendSeq[d.ID], status)
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
		// The whole report, in as many posts as FinaleChat needs to take
		// it. Every part is a report of this agent; the first keeps the key
		// and the buzz a single post had, and the figures about the turn
		// ride on the last, so a reader that shows them per post shows
		// them once, under the end of the report.
		buzz := r.relayNotifyDone[d.Name]
		if buzz {
			delete(r.relayNotifyDone, d.Name)
			r.publishRelayMeta()
		}
		parts := splitReport(body, phoneReportPart)
		for i, part := range parts {
			key := strconv.FormatInt(ev.Seq, 10)
			if i > 0 {
				key += "." + strconv.Itoa(i+1)
			}
			meta := map[string]any{"eagent": "relay", "kind": "relay_report", "relay": d.Name, "agent": agent, "seq": ev.Seq, "source_anchor": p.sourceAnchor(ev.Seq)}
			if i == len(parts)-1 {
				meta["duration_ms"], meta["tools"], meta["interrupted"], meta["images"] = d.DurationMS, d.Tools, d.Interrupted, images
			}
			if len(parts) > 1 {
				meta["part"], meta["parts"] = i+1, len(parts)
			}
			req := finalechat.PostRequest{Body: part, Notify: boolPtr(false), Meta: meta}
			if buzz && i == 0 {
				req.Notify, req.Importance = nil, "important"
			}
			p.enqueue(func(ctx context.Context) {
				// The key and the status are read here, on the worker: until
				// the first call has learned what the server supports, a key
				// made on the loop would be empty, and the post would lose
				// both its idempotency and its patient retries. An agent's
				// post without a status clears the thread's line, so each
				// part carries the one the work is in, as a message does.
				req.ClientKey = p.key("rt", key)
				act := p.attach()
				req.Activity = act
				if msg, _, err := p.client.Post(ctx, p.ref, req); err == nil {
					p.remember(msg.ID)
					p.markSent(act)
				}
			})
		}
	}
}

// phoneReportPart bounds one post of an agent's report. FinaleChat refuses
// a message body over 256 KiB and a request over 1 MiB, and JSON can write
// one byte of the body as six ("<" is \u003c), so a part of 128 KiB is
// taken whatever it holds.
const phoneReportPart = 128 << 10

// splitReport cuts a report too long for one post into parts of at most
// limit bytes, at line ends where it can and never inside a UTF-8
// sequence. When there are several, each ends with "(part i of n)" on a
// line of its own after a blank line, so its first line is the report's
// own words (the line a list shows and a reply quotes). A code block cut
// in two is closed at the end of one part and opened again at the start
// of the next with the same fence and language, so each part reads on
// its own.
func splitReport(body string, limit int) []string {
	if len(body) <= limit {
		return []string{body}
	}
	const marker = 32 // room for "\n\n_(part i of n)_"
	var parts []string
	fence, open := "", "" // the code block open where the rest starts
	lineStart := true     // the rest starts at the start of a line
	for rest := body; rest != ""; {
		head := ""
		if open != "" {
			head = open + "\n"
			if len(head) > limit/4 {
				head = fence + "\n" // an info string too long to repeat
			}
		}
		room := limit - marker
		budget := room - len(head)
		var part, nextFence, nextOpen string
		var cut int
		for {
			cut = reportCut(rest, budget)
			chunk := rest[:cut]
			nextFence, nextOpen = reportFences(chunk, lineStart, fence, open)
			part = head + chunk
			if nextFence != "" && cut < len(rest) {
				if !strings.HasSuffix(chunk, "\n") {
					part += "\n"
				}
				part += nextFence
			}
			if len(part) <= room || budget <= 1 {
				break
			}
			budget -= len(part) - room
		}
		lineStart = strings.HasSuffix(rest[:cut], "\n")
		rest = rest[cut:]
		fence, open = nextFence, nextOpen
		parts = append(parts, part)
	}
	for i := range parts {
		parts[i] += fmt.Sprintf("\n\n_(part %d of %d)_", i+1, len(parts))
	}
	return parts
}

// reportCut is where the next part of rest ends: all of it when it fits,
// else at the last line end in the second half of the budget, else at the
// budget, moved back to the start of a UTF-8 sequence. Never 0.
func reportCut(rest string, budget int) int {
	if len(rest) <= budget {
		return len(rest)
	}
	if budget < 1 {
		budget = 1
	}
	cut := budget
	if i := strings.LastIndexByte(rest[:cut], '\n'); i >= budget/2 {
		return i + 1
	}
	for cut > 0 && !utf8.RuneStart(rest[cut]) {
		cut--
	}
	if cut == 0 {
		_, cut = utf8.DecodeRuneInString(rest)
	}
	return cut
}

// reportFences follows fenced code blocks through text, as CommonMark
// reads them: a line of three or more backticks or tildes (indented at
// most three spaces) opens one, with what follows as its info string, and
// only a line of the same character, at least as long and with nothing
// after it, closes it. fence and open are the block open where text
// starts (its fence and its whole opening line); the result is the block
// open where it ends. Only whole lines count: a piece of a line cut
// between parts is not read (a fence line is short, so it is never the
// line a cut falls inside).
func reportFences(text string, lineStart bool, fence, open string) (string, string) {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if (i == 0 && !lineStart) || i == len(lines)-1 {
			// The end of a line the last part began, or what follows the
			// last line end: "" or the start of a line the next part ends.
			continue
		}
		s := strings.TrimLeft(line, " ")
		if len(line)-len(s) > 3 || len(s) < 3 || (s[0] != '`' && s[0] != '~') {
			continue
		}
		n := 0
		for n < len(s) && s[n] == s[0] {
			n++
		}
		if n < 3 {
			continue
		}
		switch {
		case fence == "":
			if s[0] == '`' && strings.ContainsRune(s[n:], '`') {
				continue // inline code, not a fence
			}
			fence, open = s[:n], strings.TrimRight(line, " \t\r")
		case s[0] == fence[0] && n >= len(fence) && strings.TrimSpace(s[n:]) == "":
			fence, open = "", ""
		}
	}
	return fence, open
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
