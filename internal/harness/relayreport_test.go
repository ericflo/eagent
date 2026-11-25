package harness

import (
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/state"
)

// relayReportSession runs a live session with a phone whose agent ends its
// first turn with report, and report_max_chars set to maxChars.
func relayReportSession(t *testing.T, report string, maxChars int) (*Runtime, *relayPhone) {
	t.Helper()
	t.Setenv("EAGENT_TEST_KEY", "x")
	seat := newFakeSeat(report)
	useFakeRelays(t, seat)
	fp := newRelayPhone()
	t.Cleanup(fp.srv.Close)
	model := newScripted(func(string, []map[string]any) reply { return hold() })
	t.Cleanup(model.srv.Close)
	project := t.TempDir()
	cfg := relayPhoneConfig(t, project, model.srv.URL, fp)
	cfg.ReportMaxChars = maxChars
	rt, err := New(cfg, Options{Project: project, Relay: "codex", Interactive: true}, &fakeUI{input: make(chan string, 4)})
	if err != nil {
		t.Fatal(err)
	}
	relayRunLive(t, rt)
	waitFor(t, "the phone poll", func() bool { return fp.getCount() > 0 })
	fp.userReply("Write up everything you found")
	return rt, fp
}

func relayReports(fp *relayPhone) []map[string]any {
	return fp.postsWhere(func(p map[string]any) bool {
		meta, _ := p["meta"].(map[string]any)
		return meta["kind"] == "relay_report"
	})
}

func recordedReport(t *testing.T, rt *Runtime) string {
	t.Helper()
	text, found := "", false
	rt.sync(func() {
		for _, ev := range rt.st.Events {
			if ev.Type == event.RelayTurn {
				var d event.RelayTurnData
				_ = ev.Decode(&d)
				text, found = d.Text, true
			}
		}
	})
	if !found {
		t.Fatal("no turn recorded")
	}
	return text
}

// The reviewer's scenario: with report_max_chars at 40000, a report of
// 22,000 characters and a final marker reaches the record and the phone
// whole, and eagent's own prompt carries all of it; at 20000 the prompt
// is cut, while the record and the phone still have it all.
func TestRelayReportReachesRecordAndPhoneWhole(t *testing.T) {
	report := strings.Repeat("abcdefghij", 2200) + "\nFINAL-MARKER"
	for _, max := range []int{40000, 20000} {
		rt, fp := relayReportSession(t, report, max)
		waitFor(t, "the agent's report on the phone", func() bool { return len(relayReports(fp)) == 1 })
		if got := recordedReport(t, rt); got != report {
			t.Fatalf("max %d: the record kept %d of %d bytes", max, len(got), len(report))
		}
		if body, _ := relayReports(fp)[0]["body"].(string); body != report {
			t.Fatalf("max %d: the phone got %d of %d bytes", max, len(body), len(report))
		}
		var prompt string
		rt.sync(func() {
			for _, m := range rt.st.View(&rt.view) {
				prompt += m.Text + "\n"
			}
		})
		switch {
		case max == 40000 && !strings.Contains(prompt, report):
			t.Errorf("max 40000: eagent's prompt does not carry the whole report")
		case max == 20000 && (strings.Contains(prompt, report) || !strings.Contains(prompt, "FINAL-MARKER")):
			// clip keeps the head and the tail.
			t.Errorf("max 20000: eagent's prompt should carry a clipped report with its end")
		}
	}
}

// A report too long for one FinaleChat message is posted in parts that each
// fit, in order, that say which part they are, and that together are the
// whole report.
func TestRelayReportTooLongForOnePostIsSplit(t *testing.T) {
	line := strings.Repeat("x", 99) + "\n"
	report := strings.Repeat(line, 3000) + "END" // about 300 KB
	rt, fp := relayReportSession(t, report, 20000)
	waitFor(t, "the report's parts", func() bool { return len(relayReports(fp)) == 3 })
	if got := recordedReport(t, rt); got != report {
		t.Fatalf("the record kept %d of %d bytes", len(got), len(report))
	}
	var joined strings.Builder
	keys := map[string]bool{}
	for i, p := range relayReports(fp) {
		body, _ := p["body"].(string)
		if len(body) > phoneReportPart || len(body) > 256<<10 {
			t.Fatalf("part %d is %d bytes", i+1, len(body))
		}
		head := "_(part " + string(rune('1'+i)) + " of 3)_\n\n"
		if !strings.HasPrefix(body, head) {
			t.Fatalf("part %d starts %q", i+1, body[:30])
		}
		meta, _ := p["meta"].(map[string]any)
		if meta["part"] != float64(i+1) || meta["parts"] != float64(3) {
			t.Errorf("part %d meta %v/%v", i+1, meta["part"], meta["parts"])
		}
		key, _ := p["client_key"].(string)
		if keys[key] {
			t.Errorf("part %d reuses the client key %q", i+1, key)
		}
		keys[key] = true
		joined.WriteString(strings.TrimPrefix(body, head))
	}
	if joined.String() != report {
		t.Fatalf("the parts join to %d bytes, want the %d of the report", joined.Len(), len(report))
	}
}

// A code block cut between two parts is closed and opened again, and a
// line with no break in it is still cut inside the limit, never inside a
// UTF-8 sequence.
func TestSplitReportKeepsCodeBlocksAndRunes(t *testing.T) {
	report := "Here is the diff:\n```\n" + strings.Repeat("+ a line of code\n", 40) + "```\nDone."
	parts := splitReport(report, 400)
	if len(parts) < 2 {
		t.Fatalf("%d parts", len(parts))
	}
	for i, p := range parts {
		if len(p) > 400 {
			t.Errorf("part %d is %d bytes", i+1, len(p))
		}
		if n := strings.Count(p, "```"); n%2 != 0 {
			t.Errorf("part %d leaves a code block open:\n%s", i+1, p)
		}
	}
	long := strings.Repeat("é", 1000) // no line breaks, two bytes a rune
	for i, p := range splitReport(long, 301) {
		if len(p) > 301 || !strings.HasSuffix(p, "é") {
			t.Errorf("part %d: %d bytes, ends %q", i+1, len(p), p[len(p)-2:])
		}
	}
	if got := splitReport("short", 400); len(got) != 1 || got[0] != "short" {
		t.Errorf("a short report: %q", got)
	}
}

// A session recorded before reports were kept whole (its report cut at
// 20000 bytes with an ellipsis) still loads and reads as it did.
func TestRelayReportClippedInAnOldSessionLoads(t *testing.T) {
	old := strings.Repeat("a", 20000) + "…"
	st := state.New()
	for _, ev := range []event.Event{
		event.New(event.RelayStart, event.ActorHarness, event.RelayStartData{Name: "codex", Profile: "codex", Agent: "codex"}),
		event.New(event.RelayTurn, event.ActorHarness, event.RelayTurnData{Name: "codex", Text: old}),
	} {
		st.Apply(ev)
	}
	if got := st.Relays["codex"].LastTurn; got != old {
		t.Fatalf("last turn %d bytes", len(got))
	}
}
