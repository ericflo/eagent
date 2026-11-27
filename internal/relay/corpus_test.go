package relay

import (
	"bufio"
	"cmp"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/store"
)

// The matcher judged against real transcripts, off unless
// EAGENT_CORPUS_SESSIONS names one or more eagent session directories
// (separated by ":"; each holds <id>/<id>.jsonl records). Every relay.send
// in the records is replayed against the transcript of the agent it went
// to, with f2b210d's rule (oldArrived, kept here as the oracle) and with
// today's, and the two are compared per agent. It reads private records:
// it prints counts and send ids only, never a word of any text.
func TestCorpusArrivalsAgainstTheOldRule(t *testing.T) {
	roots := os.Getenv("EAGENT_CORPUS_SESSIONS")
	if roots == "" {
		t.Skip("set EAGENT_CORPUS_SESSIONS to replay real sends")
	}
	type send struct {
		key, agent   string
		text, mode   string
		at, failAt   time.Time
		oldBy, newBy int // the entry that confirmed it, -1 when none
	}
	type group struct {
		agent, transcript string
		sends             []*send
	}
	groups := map[string]*group{}
	for _, root := range strings.Split(roots, ":") {
		recs, _ := filepath.Glob(filepath.Join(root, "*", "*.jsonl"))
		for _, rec := range recs {
			f, err := os.Open(rec)
			if err != nil {
				continue
			}
			cur := map[string][2]string{}
			byID := map[string]*send{}
			sc := bufio.NewScanner(f)
			sc.Buffer(make([]byte, 1<<20), 256<<20)
			for sc.Scan() {
				var ev struct {
					TS   time.Time       `json:"ts"`
					Type string          `json:"type"`
					Data json.RawMessage `json:"data"`
				}
				if json.Unmarshal(sc.Bytes(), &ev) != nil {
					continue
				}
				var d struct {
					Name, Agent, Transcript, ID, Text, Mode string
					OK                                      bool
				}
				_ = json.Unmarshal(ev.Data, &d)
				switch ev.Type {
				case "relay.start":
					cur[d.Name] = [2]string{d.Agent, d.Transcript}
				case "relay.inner":
					cur[d.Name] = [2]string{cur[d.Name][0], d.Transcript}
				case "relay.send", "relay.moved":
					c := cur[d.Name]
					if c[1] == "" || isAgentCommand(d.Text) {
						continue
					}
					key := filepath.Base(rec) + "/" + d.ID
					if old := byID[key]; old != nil {
						old.key = "" // moved: counted under its new relay only
					}
					s := &send{key: key, agent: c[0], text: strings.TrimRight(d.Text, "\n"), mode: d.Mode, at: ev.TS, oldBy: -1, newBy: -1}
					byID[key] = s
					gk := c[0] + "|" + c[1]
					if groups[gk] == nil {
						groups[gk] = &group{agent: c[0], transcript: c[1]}
					}
					groups[gk].sends = append(groups[gk].sends, s)
				case "relay.delivered":
					if s := byID[filepath.Base(rec)+"/"+d.ID]; s != nil && !d.OK && s.failAt.IsZero() {
						s.failAt = ev.TS
					}
				}
			}
			f.Close()
		}
	}
	type entry struct {
		text string
		at   time.Time
		// first: for a Claude prompt line that is a second record of a
		// queued message (queueAnalysis), the entry of its enqueue line;
		// -1 otherwise.
		first int
		again bool // the parser's mark: no arrival under today's rule
	}
	type tally struct{ replayed, oldOK, newOK, twice, seconds, secondsHit int }
	tallies := map[string]*tally{}
	var oldOnly, newOnly, twice []*send
	for _, g := range groups {
		a, err := agentFor(g.agent)
		if err != nil {
			continue
		}
		f, err := os.Open(g.transcript)
		if err != nil {
			continue
		}
		p := a.newParser(Spec{})
		var q queueAnalysis
		var entries []entry
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 256<<20)
		for sc.Scan() {
			first := -1
			if g.agent == "claude" {
				first = q.line(sc.Bytes(), len(entries))
			}
			for _, o := range p.line(sc.Bytes()) {
				if o.kind == "user" {
					entries = append(entries, entry{o.text, o.at, first, o.again})
				}
			}
		}
		f.Close()
		sort.SliceStable(g.sends, func(i, j int) bool { return g.sends[i].at.Before(g.sends[j].at) })
		// One replay per rule: a send is pending from its time (an agent
		// that stamps to the second may stamp its entry up to a second
		// before the record), until a rule confirms it, it is reported
		// failed, or half an hour passes.
		replay := func(old bool) {
			var pending []*send
			si := 0
			for ei, e := range entries {
				for si < len(g.sends) && !g.sends[si].at.After(e.at.Add(time.Second)) {
					pending = append(pending, g.sends[si])
					si++
				}
				keep := pending[:0]
				for _, s := range pending {
					if !s.failAt.IsZero() && s.failAt.Before(e.at) || e.at.Sub(s.at) > 30*time.Minute {
						continue
					}
					keep = append(keep, s)
				}
				pending = keep
				hit := map[int]bool{}
				if old {
					// f2b210d: each send tried on its own.
					got := normalize(e.text)
					for i, s := range pending {
						if want := normalize(s.text); want != "" && oldArrived(got, want) {
							hit[i] = true
							s.oldBy = ei
						}
					}
				} else if !e.again {
					views := make([]sendView, len(pending))
					for i, s := range pending {
						views[i].text = s.text
					}
					for _, i := range arrivals(views, e.text) {
						hit[i] = true
						pending[i].newBy = ei
					}
				}
				keep = pending[:0]
				for i, s := range pending {
					if !hit[i] {
						keep = append(keep, s)
					}
				}
				pending = keep
			}
		}
		replay(true)
		replay(false)
		tl := tallies[g.agent]
		if tl == nil {
			tl = &tally{}
			tallies[g.agent] = tl
		}
		confirmedBy := map[int]int{} // entry -> sends today's rule confirmed with it
		for _, s := range g.sends {
			if s.key != "" && s.newBy >= 0 {
				confirmedBy[s.newBy]++
			}
		}
		for ei, e := range entries {
			if e.first >= 0 {
				tl.seconds++
				if confirmedBy[ei] > 0 {
					tl.secondsHit++
				}
			}
		}
		for _, s := range g.sends {
			if s.key == "" {
				continue
			}
			if s.newBy >= 0 {
				// The known limit at work: confirmed by a queued message's
				// second record, whose first record had confirmed another.
				if fi := entries[s.newBy].first; fi >= 0 && confirmedBy[fi] > 0 {
					tl.twice++
					twice = append(twice, s)
				}
			}
			tl.replayed++
			if s.oldBy >= 0 {
				tl.oldOK++
			}
			if s.newBy >= 0 {
				tl.newOK++
			}
			switch {
			case s.oldBy >= 0 && s.newBy < 0:
				oldOnly = append(oldOnly, s)
			case s.newBy >= 0 && s.oldBy < 0:
				newOnly = append(newOnly, s)
			}
		}
	}
	agents := make([]string, 0, len(tallies))
	for a := range tallies {
		agents = append(agents, a)
	}
	sort.Strings(agents)
	for _, a := range agents {
		tl := tallies[a]
		no, nn := 0, 0
		for _, s := range oldOnly {
			if s.agent == a {
				no++
			}
		}
		for _, s := range newOnly {
			if s.agent == a {
				nn++
			}
		}
		t.Logf("%-12s replayed %5d  old rule %5d  today %5d  old and not today %4d  today and not old %4d  confirmed by a queued message's second record after its first confirmed another %3d", a, tl.replayed, tl.oldOK, tl.newOK, no, nn, tl.twice)
		if tl.seconds > 0 {
			t.Logf("%-12s second records of queued messages %d, of which confirmed a send %d", a, tl.seconds, tl.secondsHit)
		}
	}
	// Each difference, by id (no text).
	for _, s := range oldOnly {
		t.Logf("old and not today: %s %s", s.agent, s.key)
	}
	for _, s := range twice {
		t.Logf("second record confirmed it: %s %s", s.agent, s.key)
	}
	for _, s := range newOnly {
		why := "a later entry, the old rule having spent an earlier one on it differently"
		if s.oldBy < 0 {
			why = "pending longer under today's rule, confirmed by a later entry"
		}
		t.Logf("today and not old: %s %s (%s)", s.agent, s.key, why)
	}
}

// queueAnalysis is analysis for the corpus test only, not the seat's rule:
// it tells which of Claude's prompt lines is the second record of a
// message Claude queued while it worked (first recorded by an "enqueue"
// line). It keeps the queued messages until a "remove" names one, a meta
// line quotes one, or a prompt line is one (or a run of them, joined).
type queueAnalysis struct {
	queued []queuedEntry
}

type queuedEntry struct {
	text  string
	entry int // the entry of its enqueue line
}

// line reads one raw transcript line whose user entry, if any, will be
// entry next; for a second record it returns the first record's entry,
// otherwise -1.
func (q *queueAnalysis) line(raw []byte, next int) int {
	var ev struct {
		Type      string          `json:"type"`
		Operation string          `json:"operation"`
		IsMeta    bool            `json:"isMeta"`
		Sidechain bool            `json:"isSidechain"`
		Content   json.RawMessage `json:"content"`
		Message   *struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(raw, &ev) != nil || ev.Sidechain {
		return -1
	}
	var s string
	switch ev.Type {
	case "queue-operation":
		_ = json.Unmarshal(ev.Content, &s)
		switch ev.Operation {
		case "enqueue":
			q.queued = append(q.queued, queuedEntry{s, next})
		case "remove":
			for i, e := range q.queued {
				if e.text == s {
					q.queued = slices.Delete(q.queued, i, i+1)
					break
				}
			}
		}
		return -1
	case "user":
		if ev.Message == nil || json.Unmarshal(ev.Message.Content, &s) != nil {
			return -1
		}
	default:
		return -1
	}
	got := normalize(s)
	if ev.IsMeta {
		for i, e := range q.queued {
			if n := normalize(e.text); n != "" && strings.Contains(got, n) {
				q.queued = slices.Delete(q.queued, i, i+1)
				break
			}
		}
		return -1
	}
	if got == "" {
		return -1
	}
	for i := range q.queued {
		var joined []string
		for j := i; j < len(q.queued); j++ {
			joined = append(joined, normalize(q.queued[j].text))
			if run := strings.Join(joined, " "); run == got {
				first := q.queued[i].entry
				q.queued = slices.Delete(q.queued, i, j+1)
				return first
			} else if len(run) > len(got) {
				break
			}
		}
	}
	return -1
}

// What existing records hold at the first resume after an upgrade, off
// unless EAGENT_CORPUS_SESSIONS is set: the sends each session's record
// still waits on, in the newest holder of each relay name, by mode and by
// age at the end of the record. A resume restores them (an active relay)
// or settles them (one recorded exited), and either way they end
// unconfirmed unless the transcript shows them. Counts only.
func TestCorpusUnresolvedSendsAtTheEndOfRecords(t *testing.T) {
	roots := os.Getenv("EAGENT_CORPUS_SESSIONS")
	if roots == "" {
		t.Skip("set EAGENT_CORPUS_SESSIONS to read real records")
	}
	ages := []string{"under an hour", "under a day", "older"}
	counts := map[string]map[string]int{} // mode -> age -> sends
	var sessions, all, sessionsWith int
	fate := map[string]int{}
	for _, root := range strings.Split(roots, ":") {
		dirs, _ := filepath.Glob(filepath.Join(root, "*"))
		for _, dir := range dirs {
			evs, err := store.Read(dir)
			if err != nil || len(evs) == 0 {
				continue
			}
			sessions++
			st := state.Replay(evs)
			end := evs[len(evs)-1].Time
			sent := map[string]event.Event{} // id -> its latest send or move
			for _, ev := range evs {
				if ev.Type == event.RelaySend || ev.Type == event.RelayMoved {
					var d event.RelaySendData
					if ev.Decode(&d) == nil {
						sent[d.Name+"/"+d.ID] = ev
					}
				}
			}
			with := false
			for name, rel := range st.Relays {
				for id := range rel.Undelivered {
					ev, ok := sent[name+"/"+id]
					if !ok {
						continue
					}
					var d event.RelaySendData
					_ = ev.Decode(&d)
					mode := cmp.Or(d.Mode, "queue")
					age := ages[2]
					switch a := end.Sub(ev.Time); {
					case a < time.Hour:
						age = ages[0]
					case a < 24*time.Hour:
						age = ages[1]
					}
					if counts[mode] == nil {
						counts[mode] = map[string]int{}
					}
					counts[mode][age]++
					all++
					with = true
					switch {
					case !rel.Active():
						fate["stopped relay: left alone"]++
					case rel.State == "exited":
						fate["recorded exited: settled at the resume"]++
					default:
						fate["active: restored to the reattached seat"]++
					}
				}
			}
			if with {
				sessionsWith++
			}
		}
	}
	t.Logf("%d session records; %d end with at least one unresolved send; %d such sends", sessions, sessionsWith, all)
	for _, mode := range []string{"queue", "interrupt", "after_turn"} {
		t.Logf("%-10s  %s %4d   %s %4d   %s %4d", mode, ages[0], counts[mode][ages[0]], ages[1], counts[mode][ages[1]], ages[2], counts[mode][ages[2]])
	}
	for k, n := range fate {
		t.Logf("%s: %d", k, n)
	}
}

// oldArrived is f2b210d's rule for whether transcript text got (normalized)
// shows the send want (normalized), kept as the oracle.
func oldArrived(got, want string) bool {
	h := head(want, 160)
	for from := 0; ; {
		i := strings.Index(got[from:], h)
		if i < 0 {
			break
		}
		rest := got[from+i:]
		if strings.HasPrefix(rest, want) || strings.HasPrefix(want, strings.TrimSuffix(rest, "…")) {
			return true
		}
		from += i + 1
	}
	return len(got) > 20 && strings.Contains(want, strings.TrimSuffix(got, "…"))
}
