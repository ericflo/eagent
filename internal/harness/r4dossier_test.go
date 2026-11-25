package harness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/store"
)

// r4Instruction is something the user said before the conversation was
// restarted, which must stay in what the model sees.
const r4Instruction = "use only the archived temperature readings"

// r4RolledSession runs a session up to its first restart from a dossier
// and stops it, leaving its record on disk: the first file (ending with
// subsession.end) and the second (subsession.start, then the dossier).
func r4RolledSession(t *testing.T) (dir string, files []string) {
	t.Helper()
	var big atomic.Bool
	s := startSession(t, brief, func(_ string, msgs []map[string]any) reply {
		if ask := dossierRequest(msgs); ask != "" {
			return reply{text: briefing(places(ask)[:3])}
		}
		r := hold()
		if strings.Contains(lastUserText(msgs), "kept for you") && msgs[len(msgs)-1]["role"] != "tool" {
			r = say("Noted.")
			if big.CompareAndSwap(true, false) {
				r.prompt = 25000
			}
		}
		return r
	}, nil)
	s.waitFor("the agent's turn", func() bool { return len(s.events(event.RelayTurn)) == 1 })
	s.settled()
	big.Store(true)
	s.type_("@e " + r4Instruction)
	s.waitFor("the dossier", func() bool { return len(s.dossiers()) == 1 })
	s.settled()
	dir = s.rt.sess.Path
	s.stop()
	files, err := s.rt.sess.Files()
	if err != nil || len(files) != 2 {
		t.Fatalf("files after the restart: %v %v", files, err)
	}
	return dir, files
}

// r4Keep cuts a record file down to its first n lines, as a stop right
// after the nth write leaves it.
func r4Keep(t *testing.T, path string, n int) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.SplitAfter(string(data), "\n")
	if n > len(lines) {
		t.Fatalf("%s has %d lines, not %d", path, len(lines), n)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines[:n], "")), 0o644); err != nil {
		t.Fatal(err)
	}
}

// r4Lines counts a file's lines.
func r4Lines(t *testing.T, path string) int {
	t.Helper()
	n, err := store.LineCount(path)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// r4Resume resumes a session directory with a model that writes a
// briefing when asked for one, and keeps every request it is sent.
func r4Resume(t *testing.T, dir string) (*session, func() [][]map[string]any) {
	t.Helper()
	t.Setenv("EAGENT_TEST_KEY", "x")
	seat := newFakeSeat("ok")
	useFakeRelays(t, seat)
	var mu sync.Mutex
	var seen [][]map[string]any
	model := newScripted(func(_ string, msgs []map[string]any) reply {
		mu.Lock()
		seen = append(seen, msgs)
		mu.Unlock()
		if ask := dossierRequest(msgs); ask != "" {
			if p := places(ask); len(p) >= 3 {
				return reply{text: briefing(p[len(p)-3:])}
			}
		}
		if strings.Contains(lastUserText(msgs), "kept for you") && msgs[len(msgs)-1]["role"] != "tool" {
			return say("Here.")
		}
		return hold()
	})
	t.Cleanup(model.srv.Close)
	ui := &fakeUI{input: make(chan string, 4)}
	rt, err := Resume(testConfig(model.srv.URL), Options{Interactive: true}, ui, dir)
	if err != nil {
		t.Fatal(err)
	}
	s := &session{t: t, rt: rt, ui: ui, seat: seat, model: model, done: make(chan int, 1)}
	ctx, cancel := contextWithCancel()
	s.cancel = cancel
	go func() { s.done <- rt.Run(ctx) }()
	t.Cleanup(s.stop)
	return s, func() [][]map[string]any {
		mu.Lock()
		defer mu.Unlock()
		return append([][]map[string]any(nil), seen...)
	}
}

// r4CheckResumed resumes a record cut at some write of the restart and
// checks what must hold whatever the cut: what the user said is in every
// request the model is sent, the session ends up in a conversation that
// opens with its briefing, and every file of the record begins with
// subsession.start (after session.start, in the first).
func r4CheckResumed(t *testing.T, dir string, wantDossiers int) {
	t.Helper()
	s, requests := r4Resume(t, dir)
	s.waitFor("a conversation that opens with its briefing", func() bool {
		ok := false
		s.read(func(st *state.State) {
			cur := st.Current()
			ok = cur != nil && cur.Dossier != ""
		})
		return ok && len(s.dossiers()) >= wantDossiers
	})
	s.settled()
	s.type_("@e what next?")
	s.waitFor("the answer", func() bool { return s.ui.messageCount() >= 1 })
	s.settled()
	reqs := requests()
	if len(reqs) == 0 {
		t.Fatal("the model was never called")
	}
	for i, msgs := range reqs {
		if !strings.Contains(allText(msgs), r4Instruction) {
			t.Fatalf("request %d of %d after the resume does not hold what the user said before the restart:\n%s", i+1, len(reqs), clipRunes(allText(msgs), 3000))
		}
	}
	last := reqs[len(reqs)-1]
	if first, _ := last[1]["content"].(string); !strings.HasPrefix(first, "[Briefing.") {
		t.Fatalf("the conversation after the resume does not open with a briefing: %q", clipRunes(first, 300))
	}
	if n := len(s.dossiers()); n != wantDossiers {
		t.Fatalf("%d dossiers in the record, want %d", n, wantDossiers)
	}
	s.stop()
	files, _ := s.rt.sess.Files()
	for i, f := range files {
		evs, err := store.ReadFile(filepath.Join(dir, f))
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 && len(evs) > 0 && evs[0].Type == event.SessionStart {
			evs = evs[1:] // the session's first file opens with the session
		}
		if len(evs) == 0 || evs[0].Type != event.SubsessionStart {
			t.Fatalf("%s does not begin with subsession.start (%d events)", f, len(evs))
		}
	}
}

// A stop after the new file's subsession.start and before its dossier: the
// resumed session finishes the briefing from the record before the new
// file counts as the conversation, and nothing the user said goes missing.
func TestResumeAfterAStopBetweenTheNewFilesStartAndItsDossierFinishesTheBriefing(t *testing.T) {
	dir, files := r4RolledSession(t)
	r4Keep(t, filepath.Join(dir, files[1]), 1)
	r4CheckResumed(t, dir, 1)
}

// A stop after subsession.end was written and before anything was written
// to the new file (which exists, empty).
func TestResumeAfterAStopBetweenTheOldFilesEndAndTheNewFilesStart(t *testing.T) {
	dir, files := r4RolledSession(t)
	r4Keep(t, filepath.Join(dir, files[1]), 0)
	r4CheckResumed(t, dir, 1)
}

// A stop after the new file was made and before anything was written to
// the record: the empty file holds nothing and the session resumes as if
// the restart had not begun.
func TestResumeAfterAStopRightAfterTheNewFileWasMade(t *testing.T) {
	dir, files := r4RolledSession(t)
	old := filepath.Join(dir, files[0])
	r4Keep(t, old, r4Lines(t, old)-1) // without its subsession.end
	r4Keep(t, filepath.Join(dir, files[1]), 0)
	r4CheckResumed(t, dir, 1)
}

// The same stop twice: the run that resumed after the first was stopped
// at the same place in its own restart.
func TestASecondStopAtTheSameWriteIsHandledTheSameWay(t *testing.T) {
	dir, files := r4RolledSession(t)
	r4Keep(t, filepath.Join(dir, files[1]), 1)
	s, _ := r4Resume(t, dir)
	s.waitFor("the briefing", func() bool { return len(s.dossiers()) == 1 })
	s.settled()
	s.stop()
	all, _ := s.rt.sess.Files()
	newest := filepath.Join(dir, all[len(all)-1])
	r4Keep(t, newest, 1) // its dossier never written either
	r4CheckResumed(t, dir, 1)
}

// A stop after the dossier: the record is whole and resumes as it did.
func TestResumeAfterTheDossierIsUnchanged(t *testing.T) {
	dir, _ := r4RolledSession(t)
	before, err := store.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	st := state.Replay(before)
	if st.Current().Dossier == "" {
		t.Fatal("the record has no dossier")
	}
	view, _ := json.Marshal(st.View(nil))
	s, requests := r4Resume(t, dir)
	s.type_("@e what next?")
	s.waitFor("the answer", func() bool { return s.ui.messageCount() >= 1 })
	s.settled()
	if n := len(s.dossiers()); n != 1 {
		t.Fatalf("a whole record was restarted again: %d dossiers", n)
	}
	if reqs := requests(); len(reqs) == 0 || !strings.Contains(allText(reqs[0]), r4Instruction) {
		t.Fatal("the first request after the resume lacks what the user said")
	}
	// What the record rendered before the resume is where the conversation
	// starts now.
	var after []byte
	s.read(func(st *state.State) {
		v := st.View(nil)
		var prev []json.RawMessage
		_ = json.Unmarshal(view, &prev)
		after, _ = json.Marshal(v[:len(prev)])
	})
	if string(after) != string(view) {
		t.Fatal("the resumed conversation does not begin with what the whole record rendered")
	}
}

// r4OldSession is oldSession with its second file named for the moment it
// was made, so the files sort in the order they were written whatever
// files a resume adds.
func r4OldSession(t *testing.T) (project string, sess *store.Session, files []string) {
	t.Helper()
	t.Setenv("EAGENT_TEST_KEY", "x")
	project = t.TempDir()
	sess, err := store.Create(store.Root(project), time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	add := func(evs ...event.Event) {
		for _, ev := range evs {
			if _, err := sess.Append(ev); err != nil {
				t.Fatal(err)
			}
		}
	}
	first := sess.Current()
	add(
		event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{Session: sess.ID, Cwd: project, Models: map[string]string{"orchestrator": "glm-5", "task": "glm-5", "narrator": "glm-5"}}),
		event.New(event.SubsessionStart, event.ActorHarness, event.SubsessionStartData{File: first, Index: 0, Reason: "new"}),
		event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "write the release notes for v2"}),
		event.New(event.Assistant, event.LegacyOrchestrator, event.AssistantData{Text: "drafting", Usage: event.Usage{Input: 900, Output: 20}}),
	)
	next, err := sess.NewSubsessionName(time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	add(event.New(event.SubsessionEnd, event.ActorHarness, event.SubsessionEndData{Reason: "context full", NextFile: next, InputTokens: 151000}))
	if _, err := sess.OpenSubsession(next); err != nil {
		t.Fatal(err)
	}
	add(
		event.New(event.SubsessionStart, event.ActorHarness, event.SubsessionStartData{File: next, Index: 1, Reason: "rollover"}),
		event.New(event.Dossier, event.ActorHarness, event.DossierData{Text: "1. USER'S GOAL: release notes for v2.\n5. MAP: the request " + first + ":3"}),
		event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "keep the tone dry, no exclamation marks"}),
		event.New(event.LegacyYield, event.LegacyOrchestrator, event.LegacyYieldData{Done: false, Reason: "waiting for the user"}),
	)
	sess.Close()
	return project, sess, []string{first, next}
}

// A session of the earlier design whose upgrade was stopped after the new
// file's subsession.start and before its dossier: the next run writes the
// upgrade's dossier from the old record, as the first run set out to.
func TestUpgradeStoppedBeforeItsDossierIsWrittenOnTheNextRun(t *testing.T) {
	project, sess, files := r4OldSession(t)
	brain := func(_ string, msgs []map[string]any) reply {
		if dossierRequest(msgs) != "" {
			return reply{text: briefing([]string{files[0] + ":3", files[0] + ":4", files[1] + ":3"})}
		}
		if r, ok := startsAnAgent(msgs); ok {
			return r
		}
		return hold()
	}
	s, _ := resumeOld(t, project, sess, brain)
	s.waitFor("the upgrade's dossier", func() bool { return len(s.dossiers()) == 2 })
	s.settled()
	s.stop()
	all, _ := s.rt.sess.Files()
	if len(all) != 3 {
		t.Fatalf("files after the upgrade: %v", all)
	}
	r4Keep(t, filepath.Join(sess.Path, all[2]), 1)
	s, _ = resumeOld(t, project, sess, brain)
	s.waitFor("the upgrade's dossier, written again", func() bool { return len(s.dossiers()) == 2 })
	s.settled()
	var upgraded bool
	var subs []state.Subsession
	s.read(func(st *state.State) { upgraded, subs = st.Upgraded(), append(subs, st.Subsessions...) })
	d := s.dossiers()
	cur := subs[len(subs)-1]
	if !upgraded || cur.Reason != "upgrade" || cur.Dossier == "" || !strings.Contains(d[len(d)-1].Text, "keep the tone dry") {
		t.Fatalf("upgraded %v, current %+v", upgraded, cur)
	}
}
