package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/finalechat"
	"github.com/ericflo/eagent/internal/store"
)

// r6Thread is a fake service with one thread per session: its user
// messages, paged after an anchor in order, as FinaleChat pages them.
type r6Thread struct {
	mu      sync.Mutex
	srv     *httptest.Server
	threads map[string][]map[string]any // session id -> messages, oldest first
	pages   int
}

func newR6Thread(t *testing.T) *r6Thread {
	t.Helper()
	t.Setenv("EAGENT_FINALECHAT", "") // the gate also runs with the phone switched off
	f := &r6Thread{threads: map[string][]map[string]any{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case r.URL.Path == "/api/v1/threads":
			var out []map[string]any
			for sid, msgs := range f.threads {
				last := msgs[len(msgs)-1]
				out = append(out, map[string]any{"id": "t-" + sid, "external_id": "eagent:" + sid, "preview_sender": last["sender"], "last_activity_at": last["created_at"]})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"threads": out})
		case strings.HasPrefix(r.URL.Path, "/api/v1/threads/ext:eagent:") && strings.HasSuffix(r.URL.Path, "/messages") && r.Method == "GET":
			f.pages++
			sid := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/threads/ext:eagent:"), "/messages")
			msgs := f.threads[sid]
			after := r.URL.Query().Get("after")
			start, unknown := 0, false
			if after != "" {
				unknown = true
				for i, m := range msgs {
					if m["id"] == after {
						start, unknown = i+1, false
					}
				}
			}
			out := []map[string]any{}
			if !unknown {
				for _, m := range msgs[start:] {
					if s := r.URL.Query().Get("sender"); s == "" || m["sender"] == s {
						out = append(out, m)
					}
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"messages": out, "anchor_unknown": unknown})
		case strings.HasPrefix(r.URL.Path, "/files/"):
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("png"))
		case r.Method == "POST":
			_, _ = w.Write([]byte(`{"message":{"id":"note"},"thread":{"id":"t"}}`))
		case r.URL.Path == "/api/v1/events":
			http.Error(w, "no stream in this fixture", 503)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// say adds a message the person wrote from the app, at a time.
func (f *r6Thread) say(sid, id, body string, at time.Time, meta map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if meta == nil {
		meta = map[string]any{}
	}
	f.threads[sid] = append(f.threads[sid], map[string]any{"id": id, "sender": "user", "origin": "session", "body": body, "created_at": at, "meta": meta})
}

func (f *r6Thread) watcher(project string, host ResumeHost, t *testing.T) *resumeWatcher {
	return &resumeWatcher{project: project, host: host, client: &finalechat.Client{BaseURL: f.srv.URL, Token: "fc_fixture"}, logf: t.Logf,
		retries: &replyRetries{replies: map[string]pendingReply{}}, handled: map[string]time.Time{}}
}

func r6Info(t *testing.T, project, sid string) store.Info {
	t.Helper()
	info, err := store.Resolve(store.Root(project), sid)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

// Two messages sent from the app while the session was stopped: the sweep
// resumes the session with the earlier one; the session's poll reads on
// from it and takes the later one in turn.
func TestTheSweepResumesFromTheEarliestMessageSentWhileStopped(t *testing.T) {
	project := t.TempDir()
	sid := finishedSession(t, project, time.Now().Add(-time.Hour))
	f := newR6Thread(t)
	stop := r6Info(t, project, sid).Modified
	f.say(sid, "m1", "the checklist is A, B, C", stop.Add(time.Minute), nil)
	f.say(sid, "m2", "are you still there?", stop.Add(2*time.Minute), nil)
	host := &fakeResumeHost{hosted: map[string]bool{}}
	f.watcher(project, host, t).sweep(context.Background())
	if host.calls() != 1 || host.resumed[0].ID != "m1" || host.resumed[0].Text != "the checklist is A, B, C" {
		t.Fatalf("resumed with %#v", host.resumed)
	}
}

// Restoring a hosted session after a restart of the serve picks the same
// message the sweep would.
func TestARestoredSessionPicksTheEarliestMessageSentWhileStopped(t *testing.T) {
	project := t.TempDir()
	sid := finishedSession(t, project, time.Now().Add(-time.Hour))
	f := newR6Thread(t)
	stop := r6Info(t, project, sid).Modified
	f.say(sid, "m1", "the checklist is A, B, C", stop.Add(time.Minute), nil)
	f.say(sid, "m2", "are you still there?", stop.Add(2*time.Minute), nil)
	t.Setenv("EAGENT_FINALECHAT", "")
	t.Setenv("FINALECHAT_URL", f.srv.URL)
	t.Setenv("FINALECHAT_TOKEN", "fc_fixture")
	msg, ok, err := PendingReply(context.Background(), project, r6Info(t, project, sid))
	if err != nil || !ok || msg.ID != "m1" {
		t.Fatalf("pending reply %#v ok=%v err=%v", msg, ok, err)
	}
}

// r6Record appends events to a stopped session's record.
func r6Record(t *testing.T, project, sid string, evs ...event.Event) {
	t.Helper()
	sess, _, err := store.Open(r6Info(t, project, sid).Path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range evs {
		if _, err := sess.Append(ev); err != nil {
			t.Fatal(err)
		}
	}
	sess.Close()
}

func r6Read(id string) event.Event {
	return event.New(event.PhoneRead, event.ActorHarness, event.PhoneReadData{MessageID: id})
}

func r6Took(id, text string) event.Event {
	return event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: text, Source: "finalechat", PhoneID: id})
}

// Where a session goes on, case by case. The record keeps the ledger
// (phone.read) or does not (written before it); the thread holds what the
// person wrote. The session is resumed with want, or not at all ("").
func TestWhereAStoppedSessionGoesOnReadingItsThread(t *testing.T) {
	type said struct {
		id, body string
		at       time.Duration // from the session's last log write
		meta     map[string]any
		origin   string
	}
	card := map[string]any{"kind": "answer", "question_id": "card-1"}
	for _, c := range []struct {
		name   string
		record []event.Event
		thread []said
		want   string
	}{
		{"one message, by time", nil, []said{{"m1", "go on", time.Minute, nil, ""}}, "m1"},
		{"three messages, by time: the first", nil, []said{{"m1", "a", time.Minute, nil, ""}, {"m2", "b", 2 * time.Minute, nil, ""}, {"m3", "c", 3 * time.Minute, nil, ""}}, "m1"},
		{"messages from before the stop are not taken again, by time", nil, []said{{"m0", "old", -time.Hour, nil, ""}}, ""},
		{"an older message, then a new one, by time", nil, []said{{"m0", "old", -time.Hour, nil, ""}, {"m1", "new", time.Minute, nil, ""}}, "m1"},
		{"an agent's own post and a taken-back message are passed over", nil, []said{{"x1", "mirror", time.Minute, nil, "token"}, {"d1", "never mind", 90 * time.Second, nil, "deleted"}, {"m1", "go on", 2 * time.Minute, nil, ""}}, "m1"},
		{"a run by an eagent without the ledger is read by time", []event.Event{r6Read("start"), r6Took("m1", "a"), event.New(event.SessionResume, event.ActorHarness, event.SessionResumeData{})}, []said{{"start", "", -time.Hour, nil, "token"}, {"m1", "a", -2 * time.Hour, nil, ""}, {"q1", "/quit", -time.Minute, nil, ""}}, ""},
		{"a run by an eagent without the ledger, then a new message", []event.Event{r6Read("start"), r6Took("m1", "a"), event.New(event.SessionResume, event.ActorHarness, event.SessionResumeData{})}, []said{{"start", "", -time.Hour, nil, "token"}, {"m1", "a", -2 * time.Hour, nil, ""}, {"q1", "/quit", -time.Minute, nil, ""}, {"m2", "b", time.Minute, nil, ""}}, "m2"},
		{"ledger: after the last message taken in", []event.Event{r6Read("start"), r6Took("m1", "a")}, []said{{"start", "", -time.Hour, nil, "token"}, {"m1", "a", -time.Minute, nil, ""}, {"m2", "b", time.Minute, nil, ""}}, "m2"},
		{"ledger: a message received just before a hard stop is not lost", []event.Event{r6Read("m1")}, []said{{"m1", "a", -time.Hour, nil, ""}, {"m2", "b", -time.Second, nil, ""}, {"m3", "c", time.Minute, nil, ""}}, "m2"},
		{"ledger: everything taken in", []event.Event{r6Read("m1"), r6Took("m2", "b")}, []said{{"m1", "a", -time.Hour, nil, ""}, {"m2", "b", -time.Minute, nil, ""}}, ""},
		{"ledger: an answer whose card was taken another way", []event.Event{r6Read("m1"), event.New(event.PhoneAnswerTaken, event.ActorHarness, event.PhoneAnswerTakenData{CardID: "card-1"})}, []said{{"m1", "a", -time.Hour, nil, ""}, {"a1", "blue", time.Minute, card, ""}, {"m2", "b", 2 * time.Minute, nil, ""}}, "m2"},
		{"ledger: the cursor's message is gone, by time", []event.Event{r6Read("gone")}, []said{{"m0", "old", -time.Hour, nil, ""}, {"m1", "new", time.Minute, nil, ""}}, "m1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			project := t.TempDir()
			sid := finishedSession(t, project, time.Now().Add(-2*time.Hour))
			if len(c.record) > 0 {
				r6Record(t, project, sid, c.record...)
			}
			f := newR6Thread(t)
			stop := r6Info(t, project, sid).Modified
			for _, m := range c.thread {
				f.say(sid, m.id, m.body, stop.Add(m.at), m.meta)
				if m.origin != "" {
					f.mu.Lock()
					last := f.threads[sid][len(f.threads[sid])-1]
					if m.origin == "deleted" {
						last["deleted"] = true // a tombstone: the person took it back
					} else {
						last["origin"] = m.origin
					}
					f.mu.Unlock()
				}
			}
			host := &fakeResumeHost{hosted: map[string]bool{}}
			f.watcher(project, host, t).sweep(context.Background())
			got := ""
			if host.calls() > 0 {
				got = host.resumed[0].ID
			}
			if got != c.want || host.calls() > 1 {
				t.Fatalf("resumed with %q (%d resumes), want %q", got, host.calls(), c.want)
			}
		})
	}
}

// A message that brings the thread up on the event stream resumes the
// session from the earliest one it has not taken in, not from itself.
func TestAMessageOnTheStreamResumesFromTheEarliestNotTakenIn(t *testing.T) {
	project := t.TempDir()
	sid := finishedSession(t, project, time.Now().Add(-time.Hour))
	f := newR6Thread(t)
	stop := r6Info(t, project, sid).Modified
	f.say(sid, "m1", "the checklist is A, B, C", stop.Add(time.Minute), nil)
	f.say(sid, "m2", "are you still there?", stop.Add(2*time.Minute), nil)
	host := &fakeResumeHost{hosted: map[string]bool{}}
	w := f.watcher(project, host, t)
	w.consider(context.Background(), finalechat.Thread{ID: "t-" + sid, ExternalID: "eagent:" + sid}, finalechat.Message{ID: "m2", Sender: "user", Origin: "session", Body: "are you still there?"})
	if host.calls() != 1 || host.resumed[0].ID != "m1" {
		t.Fatalf("resumed with %#v", host.resumed)
	}
}

// A file sent first and words second: the session is resumed with the
// file's message, the file saved under the session.
func TestAFileSentFirstResumesTheSessionWithTheFile(t *testing.T) {
	project := t.TempDir()
	sid := finishedSession(t, project, time.Now().Add(-time.Hour))
	f := newR6Thread(t)
	stop := r6Info(t, project, sid).Modified
	f.say(sid, "m1", "", stop.Add(time.Minute), nil)
	f.mu.Lock()
	f.threads[sid][0]["attachments"] = []map[string]any{{"id": "att-1", "kind": "image", "content_type": "image/png", "filename": "shot.png", "size": 3, "url": "/files/att-1"}}
	f.mu.Unlock()
	f.say(sid, "m2", "see the screenshot", stop.Add(2*time.Minute), nil)
	host := &fakeResumeHost{hosted: map[string]bool{}}
	f.watcher(project, host, t).sweep(context.Background())
	if host.calls() != 1 || host.resumed[0].ID != "m1" || len(host.resumed[0].Attachments) != 1 {
		t.Fatalf("resumed with %#v", host.resumed)
	}
}

// Two sweeps at once (the serve retrying) resume the session once.
func TestTwoSweepsAtOnceResumeTheSessionOnce(t *testing.T) {
	project := t.TempDir()
	sid := finishedSession(t, project, time.Now().Add(-time.Hour))
	f := newR6Thread(t)
	stop := r6Info(t, project, sid).Modified
	f.say(sid, "m1", "the checklist is A, B, C", stop.Add(time.Minute), nil)
	f.say(sid, "m2", "are you still there?", stop.Add(2*time.Minute), nil)
	host := &fakeResumeHost{hosted: map[string]bool{}}
	w := f.watcher(project, host, t)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); w.sweep(context.Background()) }()
	}
	wg.Wait()
	w.sweep(context.Background())
	if host.calls() != 1 || host.resumed[0].ID != "m1" {
		t.Fatalf("resumed %d times: %#v", host.calls(), host.resumed)
	}
}
