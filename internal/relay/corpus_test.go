package relay

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
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
		oldByAgain   bool
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
		text  string
		at    time.Time
		again bool
	}
	type tally struct{ replayed, oldOK, newOK int }
	tallies := map[string]*tally{}
	var oldOnly, newOnly []*send
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
		var entries []entry
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 256<<20)
		for sc.Scan() {
			for _, o := range p.line(sc.Bytes()) {
				if o.kind == "user" {
					entries = append(entries, entry{o.text, o.at, o.again})
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
					// f2b210d: no second-arrival rule, each send tried on its own.
					got := normalize(e.text)
					for i, s := range pending {
						if want := normalize(s.text); want != "" && oldArrived(got, want) {
							hit[i] = true
							s.oldBy, s.oldByAgain = ei, e.again
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
		for _, s := range g.sends {
			if s.key == "" {
				continue
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
		t.Logf("%-12s replayed %5d  old rule %5d  today %5d  old and not today %4d  today and not old %4d", a, tl.replayed, tl.oldOK, tl.newOK, no, nn)
	}
	// Each difference, by id, with what kind it is (no text).
	for _, s := range oldOnly {
		why := "the entry's place was claimed by another send"
		if s.oldByAgain {
			why = "the old rule took Claude's second line of a queued message for an arrival"
		}
		t.Logf("old and not today: %s %s (%s)", s.agent, s.key, why)
	}
	for _, s := range newOnly {
		why := "a later entry, the old rule having spent an earlier one on it differently"
		if s.oldBy < 0 {
			why = "pending longer under today's rule, confirmed by a later entry"
		}
		t.Logf("today and not old: %s %s (%s)", s.agent, s.key, why)
	}
}

// Claude's queued messages judged against real transcripts, off unless
// EAGENT_CORPUS_CLAUDE names a Claude projects directory: a message Claude
// queued arrives at its enqueue line, and the parser marks its later prompt
// line as the same arrival. Counts only.
func TestCorpusClaudeQueuedMessagesArriveOnce(t *testing.T) {
	root := os.Getenv("EAGENT_CORPUS_CLAUDE")
	if root == "" {
		t.Skip("set EAGENT_CORPUS_CLAUDE to read real Claude transcripts")
	}
	files, queued, again, marked, typed := 0, 0, 0, 0, 0
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".jsonl") || strings.Contains(path, "/subagents/") {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()
		files++
		p := &claudeParser{}
		enq, seen := map[string]int{}, map[string]int{}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 256<<20)
		for sc.Scan() {
			isEnq := strings.Contains(sc.Text(), `"operation":"enqueue"`)
			for _, o := range p.line(sc.Bytes()) {
				if o.kind != "user" {
					continue
				}
				if o.again {
					marked++
					continue
				}
				n := normalize(o.text)
				if isEnq {
					enq[n]++
					queued++
				}
				seen[n]++
			}
		}
		for n, k := range enq {
			if seen[n] > k {
				again++
				if !strings.HasPrefix(n, "<") {
					typed++
				}
			}
		}
		return nil
	})
	t.Logf("%d transcripts, %d queued arrivals, %d prompt lines marked as the same arrival, %d queued texts that still arrive again (%d not the agent's own <tag> notices)", files, queued, marked, again, typed)
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
