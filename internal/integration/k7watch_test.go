package integration

import (
	"context"
	"testing"
	"time"
)

// k7Kept is a stopped session whose thread has a reply (m1) that could not
// resume it: the watcher keeps a copy, and the thread was told it keeps
// trying every minute.
func k7Kept(t *testing.T, body string) (*r6Thread, *resumeWatcher, *flakyResumeHost, string, string) {
	t.Helper()
	project := t.TempDir()
	sid := finishedSession(t, project, time.Now().Add(-time.Hour))
	r6Record(t, project, sid, r6Run(), r6Read("start"))
	f := newR6Thread(t)
	stop := r6Info(t, project, sid).Modified
	f.say(sid, "start", "", stop.Add(-time.Hour), nil)
	f.say(sid, "m1", body, stop.Add(time.Minute), nil)
	host := &flakyResumeHost{fakeResumeHost: fakeResumeHost{hosted: map[string]bool{}}, failures: 1}
	w := f.watcher(project, host, t)
	w.sweep(context.Background())
	if p := w.retries.replies["m1"]; host.calls() != 0 || !p.told {
		t.Fatalf("after the failed resume: resumed %#v, kept %#v", host.resumed, w.retries.replies)
	}
	return f, w, host, project, sid
}

// A reply whose resume failed is taken back in the app. The session is
// then run from the terminal, and that run takes in a later message, so
// its cursor stands past the deleted reply. The next sweep does not resume
// the session with the kept copy: the thread no longer has the message,
// and the copy is let go.
func TestK7ADeletedReplyPassedByTheCursorIsNotResumedFromItsKeptCopy(t *testing.T) {
	f, w, host, project, sid := k7Kept(t, "drop the staging database")
	f.mu.Lock()
	f.threads[sid][1]["deleted"] = true
	f.threads[sid][1]["body"] = ""
	f.mu.Unlock()
	f.say(sid, "m2", "never mind that, run the tests", time.Now(), nil)
	r6Record(t, project, sid, r6Run(), r6Read("start"), r6Took("m2", "never mind that, run the tests"))
	w.sweep(context.Background())
	if host.calls() != 0 || len(w.retries.replies) != 0 {
		t.Fatalf("after the reply was taken back and a run read past it: resumed %#v, kept %#v", host.resumed, w.retries.replies)
	}
}

// The kept copy of a reply the thread still has resumes the session at the
// next sweep, as it always did.
func TestK7AKeptReplyStillInTheThreadIsTriedAgain(t *testing.T) {
	_, w, host, _, _ := k7Kept(t, "please deploy the fix")
	w.sweep(context.Background())
	if host.calls() != 1 || host.resumed[0].ID != "m1" || host.resumed[0].Text != "please deploy the fix" {
		t.Fatalf("resumed %#v, kept %#v; want the kept reply", host.resumed, w.retries.replies)
	}
}

// Two replies wait while the session is stopped. The resume with the first
// fails: the watcher keeps it and tells the thread it keeps trying, and
// that note is now the thread's newest message, so the sweep's list of
// threads passes the thread over. The user takes the first reply back.
// The second still waits untaken: the sweep that lets the first go reads
// the thread and resumes the session with the second.
func TestK7TakingBackTheKeptReplyLeavesTheReplyAfterItToBeTaken(t *testing.T) {
	f, w, host, _, sid := k7Kept(t, "drop the staging database")
	f.say(sid, "m2", "and then run the tests", time.Now(), nil)
	f.mu.Lock()
	f.threads[sid] = append(f.threads[sid], map[string]any{"id": "note", "sender": "system", "origin": "token", "body": "eagent could not resume this session for your reply yet. It keeps trying every minute.", "created_at": time.Now(), "meta": map[string]any{}})
	f.threads[sid][1]["deleted"] = true
	f.threads[sid][1]["body"] = ""
	f.mu.Unlock()
	w.sweep(context.Background())
	if _, kept := w.retries.replies["m1"]; host.calls() != 1 || host.resumed[0].ID != "m2" || kept {
		t.Fatalf("resumed %#v, m1 still kept: %v; want m1 let go and the session resumed with m2, the reply after it", host.resumed, kept)
	}
}
