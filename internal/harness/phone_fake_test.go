package harness

// The fake Finalechat the phone tests run against, brought over from the
// old phone_test.go (a minimal service: one thread, messages, questions,
// the long-poll shape the mirror relies on), and a rig that starts a
// session with the mirror switched on for that one test.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/store"
)

// phoneFake is a minimal Finalechat: one thread, messages, questions, and
// the long-poll shape the runtime relies on.
type phoneFake struct {
	srv       *httptest.Server
	mu        sync.Mutex
	messages  []map[string]any
	questions map[string]map[string]any
	posts     []map[string]any // bodies of POST …/messages
	asks      []map[string]any // bodies of POST …/questions
	cancelled []string
	// dismissedByToken lists questions closed through POST …/dismiss.
	dismissedByToken []string
	seq              int
	remote           bool
	cond             *sync.Cond
	files            map[string]phoneFakeFile // attachment id -> bytes served to the agent
	uploaded         []phoneFakeFile          // files the agent posted
	features         []string                 // what /me advertises
	activity         []map[string]any         // bodies of POST …/activity
	cleared          int                      // DELETE …/activity calls
	keys             map[string]string        // client_key -> message id
	qkeys            map[string]string        // client_key -> question id, whatever its status (as the service keeps it)
	dupes            int                      // posts answered from a known key
	metas            []map[string]any         // PATCH thread meta bodies
	patches          []map[string]any         // PATCH thread bodies (retitles)
	latency          time.Duration            // added to every request, to imitate a real network
	anchorUnknown    bool                     // every GET …/messages answers at once with an unknown anchor
	gets             int                      // GET …/messages calls
	after            string                   // the anchor of the latest GET …/messages
	// The thread's title and summary as the app would show them, with the
	// service's pin rule: what the user wrote stays, whatever a token sends.
	title, desc             string
	titlePinned, descPinned bool
	// The status line as the service holds it: what shows, until when,
	// the seq it was set with, and the watermark of the latest clear.
	live                *phoneFakeStatus
	liveSeq, clearedSeq float64
	// rejectActivity makes every POST …/activity answer applied=false,
	// as the service does when a newer status is live.
	rejectActivity bool
	// questionsDown makes every GET …/questions/<id> fail as a service
	// that cannot be reached does.
	questionsDown bool
}

// threadJSON is the thread as the service returns it. Call with f.mu held.
func (f *phoneFake) threadJSON() map[string]any {
	return map[string]any{"id": "t1", "external_id": "eagent:x", "title": f.title, "description": f.desc, "summary": f.desc, "title_pinned": f.titlePinned, "description_pinned": f.descPinned}
}

// rename is the user renaming the thread in the app, which pins the title.
func (f *phoneFake) rename(title string) {
	f.mu.Lock()
	f.title, f.titlePinned = title, title != ""
	f.mu.Unlock()
}

func (f *phoneFake) threadTitle() (title, desc string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.title, f.desc
}

type phoneFakeStatus struct {
	text, kind string
	expires    time.Time
}

// setStatus applies a status line as the service does; seq orders it.
// force is a post carrying the line, which the service applies whatever
// the seqs say. Call with f.mu held. It reports whether it applied.
func (f *phoneFake) setStatus(a map[string]any, force bool) bool {
	text, _ := a["text"].(string)
	kind, _ := a["kind"].(string)
	seq, _ := a["seq"].(float64)
	ttl, _ := a["ttl_seconds"].(float64)
	if ttl == 0 {
		ttl = 90
	}
	if text == "" {
		f.clearStatus(seq)
		return true
	}
	now := time.Now()
	cur := f.live
	if cur != nil && !now.Before(cur.expires) {
		cur = nil
	}
	if !force {
		if cur != nil && cur.text == text && cur.kind == kind && cur.expires.Sub(now) > time.Duration(ttl)*time.Second/2 {
			return false // a repeat with most of its life left
		}
		if seq != 0 && (seq <= f.clearedSeq || (cur != nil && seq <= f.liveSeq)) {
			return false // a newer clear, or a newer status still live
		}
	}
	f.live = &phoneFakeStatus{text: text, kind: kind, expires: now.Add(time.Duration(ttl) * time.Second)}
	f.liveSeq = max(f.liveSeq, seq)
	f.cond.Broadcast()
	return true
}

// clearStatus drops the line, keeping the highest clear's seq. Call with
// f.mu held.
func (f *phoneFake) clearStatus(seq float64) {
	f.live = nil
	f.clearedSeq = max(f.clearedSeq, seq)
	f.cond.Broadcast()
}

// postStatus is what a post does to the line: one carrying a status sets
// it, the user's own words leave it, anything else clears it. Call with
// f.mu held.
func (f *phoneFake) postStatus(body map[string]any, sender string) {
	if a, ok := body["activity"].(map[string]any); ok {
		f.setStatus(a, true)
		return
	}
	if sender != "user" {
		seq, _ := body["clear_seq"].(float64)
		f.clearStatus(seq)
	}
}

// liveJSON is the line as a thread carries it. Call with f.mu held.
func (f *phoneFake) liveJSON() any {
	if f.live == nil || !time.Now().Before(f.live.expires) {
		return nil
	}
	return map[string]any{"text": f.live.text, "kind": f.live.kind}
}

type phoneFakeFile struct {
	name, contentType string
	data              []byte
}

func newPhoneFake(remote bool) *phoneFake {
	f := &phoneFake{questions: map[string]map[string]any{}, remote: remote, files: map[string]phoneFakeFile{}, keys: map[string]string{}, qkeys: map[string]string{}, features: []string{"activity", "idempotency", "dismiss", "attachments"}}
	f.cond = sync.NewCond(&f.mu)
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	return f
}

func (f *phoneFake) nextID() string { f.seq++; return fmt.Sprintf("id-%04d", f.seq) }

func (f *phoneFake) handle(w http.ResponseWriter, r *http.Request) {
	if f.latency > 0 {
		time.Sleep(f.latency)
	}
	if r.Header.Get("Authorization") != "Bearer fc_test" {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":{"code":"unauthorized","message":"bad token"}}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	var body map[string]any
	path := r.URL.Path
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			w.WriteHeader(400)
			return
		}
		body = map[string]any{}
		for k, v := range r.MultipartForm.Value {
			if len(v) > 0 {
				body[k] = v[0]
			}
		}
		for _, k := range []string{"meta", "activity"} {
			if raw, ok := body[k].(string); ok {
				var obj map[string]any
				if json.Unmarshal([]byte(raw), &obj) == nil {
					body[k] = obj
				}
			}
		}
		var atts []map[string]any
		for _, hs := range r.MultipartForm.File {
			for _, h := range hs {
				fh, _ := h.Open()
				data, _ := io.ReadAll(fh)
				fh.Close()
				f.mu.Lock()
				f.uploaded = append(f.uploaded, phoneFakeFile{name: h.Filename, contentType: h.Header.Get("Content-Type"), data: data})
				f.mu.Unlock()
				atts = append(atts, map[string]any{"id": "up-" + h.Filename, "kind": "file", "content_type": h.Header.Get("Content-Type"), "filename": h.Filename, "size": len(data), "url": "/api/v1/attachments/up-" + h.Filename})
			}
		}
		body["_attachments"] = atts
	} else if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	switch {
	case strings.HasPrefix(path, "/api/v1/attachments/"):
		id := strings.TrimPrefix(path, "/api/v1/attachments/")
		f.mu.Lock()
		file, ok := f.files[id]
		f.mu.Unlock()
		if !ok {
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"no attachment"}}`))
			return
		}
		w.Header().Set("Content-Type", file.contentType)
		w.Header().Set("Content-Disposition", `inline; filename="`+file.name+`"`)
		_, _ = w.Write(file.data)
	case path == "/api/v1/me":
		_ = json.NewEncoder(w).Encode(map[string]any{"auth": "token", "base_url": f.srv.URL, "push_enabled": true, "version": "test", "features": f.features, "user": map[string]any{"display_name": "Tester", "email": "t@x", "settings": map[string]any{"notify_all_messages": false, "remote_mode": f.remote}}})
	case strings.HasSuffix(path, "/messages") && r.Method == http.MethodPost:
		f.mu.Lock()
		if key, _ := body["client_key"].(string); key != "" {
			if id, ok := f.keys[key]; ok {
				f.dupes++
				var orig map[string]any
				for _, m := range f.messages {
					if m["id"] == id {
						orig = m
					}
				}
				f.mu.Unlock()
				_ = json.NewEncoder(w).Encode(map[string]any{"message": orig, "thread": map[string]any{"id": "t1"}, "created": false})
				return
			}
		}
		sender, _ := body["sender"].(string)
		if sender == "" {
			sender = "agent"
		}
		m := map[string]any{"id": f.nextID(), "thread_id": "t1", "sender": sender, "body": body["body"], "format": "markdown", "importance": body["importance"], "meta": body["meta"], "origin": "token", "created_at": time.Now().UTC().Format(time.RFC3339Nano)}
		if key, _ := body["client_key"].(string); key != "" {
			f.keys[key] = m["id"].(string)
		}
		if m["importance"] == nil {
			m["importance"] = "normal"
		}
		if atts, ok := body["_attachments"].([]map[string]any); ok && len(atts) > 0 {
			m["attachments"] = atts
			delete(body, "_attachments")
		}
		f.messages = append(f.messages, m)
		f.posts = append(f.posts, body)
		f.postStatus(body, sender)
		// Only the post that creates the thread names it.
		if title, _ := body["title"].(string); f.title == "" && len(f.posts) == 1 {
			f.title = title
		}
		thread := f.threadJSON()
		thread["agent"] = body["agent"]
		f.cond.Broadcast()
		f.mu.Unlock()
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{"message": m, "thread": thread})
	case strings.HasSuffix(path, "/messages") && r.Method == http.MethodGet:
		f.mu.Lock()
		f.gets++
		f.after = r.URL.Query().Get("after")
		unknown := f.anchorUnknown
		f.mu.Unlock()
		if unknown {
			_ = json.NewEncoder(w).Encode(map[string]any{"messages": []any{}, "has_more": false, "thread_id": "t1", "timed_out": false, "anchor_unknown": true})
			return
		}
		after := r.URL.Query().Get("after")
		sender := r.URL.Query().Get("sender")
		wait := 0
		fmt.Sscanf(r.URL.Query().Get("wait"), "%d", &wait)
		deadline := time.Now().Add(time.Duration(wait) * time.Second)
		defer wakeAt(r, &f.mu, f.cond, deadline)()
		f.mu.Lock()
		var out []map[string]any
		for {
			out = nil
			seen := after == ""
			for _, m := range f.messages {
				if !seen {
					seen = m["id"] == after
					continue
				}
				if sender == "" || m["sender"] == sender {
					out = append(out, m)
				}
			}
			if len(out) > 0 || wait == 0 || time.Now().After(deadline) {
				break
			}
			// wake on new messages or every 200ms to re-check the deadline
			f.cond.Wait()
			if r.Context().Err() != nil {
				f.mu.Unlock()
				return
			}
		}
		f.mu.Unlock()
		if out == nil {
			out = []map[string]any{}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"messages": out, "has_more": false, "thread_id": "t1", "timed_out": len(out) == 0})
	case strings.HasSuffix(path, "/questions") && r.Method == http.MethodPost:
		f.mu.Lock()
		// The service answers a known key with the question it made for it,
		// whatever has become of that question since (expired, cancelled,
		// dismissed): no new card, and the status line is left alone.
		if key, _ := body["client_key"].(string); key != "" {
			if id, ok := f.qkeys[key]; ok {
				f.asks = append(f.asks, body)
				q := phoneCopy(f.questions[id])
				f.mu.Unlock()
				_ = json.NewEncoder(w).Encode(map[string]any{"question": q, "thread": map[string]any{"id": "t1"}, "created": false})
				return
			}
		}
		q := map[string]any{"id": f.nextID(), "thread_id": "t1", "prompt": body["prompt"], "options": body["options"], "allow_freeform": true, "multi_select": false, "status": "pending", "answer": nil, "meta": body["meta"], "created_at": time.Now().UTC().Format(time.RFC3339Nano), "expires_at": nil}
		f.questions[q["id"].(string)] = q
		if key, _ := body["client_key"].(string); key != "" {
			f.qkeys[key] = q["id"].(string)
		}
		f.asks = append(f.asks, body)
		f.postStatus(body, "agent")
		f.cond.Broadcast()
		q = phoneCopy(q)
		f.mu.Unlock()
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{"question": q, "thread": map[string]any{"id": "t1"}, "created": true})
	case strings.HasPrefix(path, "/api/v1/questions/") && strings.HasSuffix(path, "/dismiss"):
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/api/v1/questions/"), "/dismiss")
		f.mu.Lock()
		q := f.questions[id]
		if q == nil || q["status"] != "pending" {
			f.mu.Unlock()
			w.WriteHeader(409)
			_, _ = w.Write([]byte(`{"error":{"code":"already_resolved","message":"resolved"}}`))
			return
		}
		q["status"] = "dismissed"
		q["answered_at"] = time.Now().UTC().Format(time.RFC3339Nano) // as the service stamps a resolution
		f.dismissedByToken = append(f.dismissedByToken, id)
		f.cond.Broadcast()
		q = phoneCopy(q)
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"question": q})
	case strings.HasPrefix(path, "/api/v1/questions/") && strings.HasSuffix(path, "/cancel"):
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/api/v1/questions/"), "/cancel")
		f.mu.Lock()
		f.cancelled = append(f.cancelled, id)
		if q := f.questions[id]; q != nil && q["status"] == "pending" {
			q["status"] = "cancelled"
			q["answered_at"] = time.Now().UTC().Format(time.RFC3339Nano) // as the service stamps a resolution
		}
		f.cond.Broadcast()
		q := phoneCopy(f.questions[id])
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"question": q})
	case strings.HasPrefix(path, "/api/v1/questions/"):
		id := strings.TrimPrefix(path, "/api/v1/questions/")
		f.mu.Lock()
		down := f.questionsDown
		f.mu.Unlock()
		if down {
			w.WriteHeader(503)
			_, _ = w.Write([]byte(`{"error":{"code":"unavailable","message":"down"}}`))
			return
		}
		wait := 0
		fmt.Sscanf(r.URL.Query().Get("wait"), "%d", &wait)
		deadline := time.Now().Add(time.Duration(wait) * time.Second)
		defer wakeAt(r, &f.mu, f.cond, deadline)()
		f.mu.Lock()
		q := f.questions[id]
		for q != nil && q["status"] == "pending" && wait > 0 && time.Now().Before(deadline) {
			f.cond.Wait()
			if r.Context().Err() != nil {
				f.mu.Unlock()
				return
			}
		}
		q = phoneCopy(q)
		f.mu.Unlock()
		if q == nil {
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"no question"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"question": q})
	case strings.HasSuffix(path, "/activity") && r.Method == http.MethodPost:
		f.mu.Lock()
		if len(f.messages) == 0 {
			f.mu.Unlock()
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"no thread"}}`))
			return
		}
		f.activity = append(f.activity, body)
		applied := !f.rejectActivity && f.setStatus(body, false)
		shown := f.liveJSON()
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"applied": applied, "thread": map[string]any{"id": "t1", "activity": shown}})
	case strings.HasSuffix(path, "/activity") && r.Method == http.MethodDelete:
		f.mu.Lock()
		f.cleared++
		var seq float64
		fmt.Sscanf(r.URL.Query().Get("seq"), "%g", &seq)
		f.clearStatus(seq)
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"thread": map[string]any{"id": "t1"}})
	case strings.HasPrefix(path, "/api/v1/threads/") && r.Method == http.MethodPatch:
		f.mu.Lock()
		f.patches = append(f.patches, body)
		if meta, ok := body["meta"].(map[string]any); ok {
			f.metas = append(f.metas, meta)
		}
		if title, _ := body["title"].(string); title != "" && !f.titlePinned {
			f.title = title
		}
		if desc, _ := body["description"].(string); desc != "" && !f.descPinned {
			f.desc = desc
		}
		thread := f.threadJSON()
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"thread": thread})
	case strings.HasPrefix(path, "/api/v1/threads/") && r.Method == http.MethodGet && strings.Count(path, "/") == 4:
		f.mu.Lock()
		thread, exists := f.threadJSON(), f.title != "" || len(f.posts) > 0
		f.mu.Unlock()
		if !exists {
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"no thread"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"thread": thread})
	default:
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"` + path + `"}}`))
	}
}

// wakeAt wakes a long poll waiting on cond when its request ends or its
// deadline passes, so it neither outlives its client nor oversleeps; the
// changes it waits for broadcast on their own. The returned func stops it.
func wakeAt(r *http.Request, mu *sync.Mutex, cond *sync.Cond, deadline time.Time) func() {
	wake := func() { mu.Lock(); cond.Broadcast(); mu.Unlock() }
	stopCtx := context.AfterFunc(r.Context(), wake)
	timer := time.AfterFunc(time.Until(deadline), wake)
	return func() { stopCtx(); timer.Stop() }
}

// userReply is the user typing on the phone.
func (f *phoneFake) userReply(text string) { f.userReplyWith(text, nil) }

// userReplyWith is the user sending words and/or files from the phone.
func (f *phoneFake) userReplyWith(text string, atts []map[string]any) {
	f.mu.Lock()
	m := map[string]any{"id": f.nextID(), "thread_id": "t1", "sender": "user", "origin": "session", "body": text, "format": "text", "importance": "normal", "meta": map[string]any{}, "created_at": time.Now().UTC().Format(time.RFC3339Nano)}
	if len(atts) > 0 {
		m["attachments"] = atts
	}
	f.messages = append(f.messages, m)
	f.cond.Broadcast()
	f.mu.Unlock()
}

func (f *phoneFake) uploads() []phoneFakeFile {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]phoneFakeFile{}, f.uploaded...)
}

// answer is the user tapping an option: the question resolves and a user
// message records the choice, as the real service does.
func (f *phoneFake) answer(label, text string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, q := range f.questions {
		if q["status"] != "pending" {
			continue
		}
		q["status"] = "answered"
		q["answered_at"] = time.Now().UTC().Format(time.RFC3339Nano) // as the service stamps a resolution
		q["answer"] = map[string]any{"selected": []string{label}, "text": text}
		body := label
		if text != "" {
			body = label + " — " + text
		}
		f.messages = append(f.messages, map[string]any{"id": f.nextID(), "thread_id": "t1", "sender": "user", "body": body, "format": "text", "importance": "normal", "meta": map[string]any{"kind": "answer", "question_id": q["id"]}, "created_at": time.Now().UTC().Format(time.RFC3339Nano)})
		f.cond.Broadcast()
		return true
	}
	return false
}

func (f *phoneFake) pendingQuestions() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, q := range f.questions {
		if q["status"] == "pending" {
			n++
		}
	}
	return n
}

func (f *phoneFake) postCount() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.posts) }

func (f *phoneFake) postsWhere(pred func(map[string]any) bool) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []map[string]any
	for _, p := range f.posts {
		if pred(p) {
			out = append(out, p)
		}
	}
	return out
}

// phoneFakeConfig points the mirror at the fake service and lifts the
// package-wide kill switch for this test only. It also pins the TempDir
// project closed for artifact publication, in memory and on disk: Run only
// starts the publisher when the run's own configuration enables artifacts,
// and the publisher's sweep re-reads the project config from disk, where a
// bare TempDir would mean defaults (artifacts ON) and credential resolution
// would fall back to the developer's production
// ~/.config/finalechat/config.json. Either pin alone stops that; both are
// set so neither path can leak again.
func phoneFakeConfig(t *testing.T, project, modelURL string, fp *phoneFake) config.Config {
	t.Helper()
	t.Setenv("EAGENT_FINALECHAT", "on")
	cfg := testConfig(modelURL)
	on := true
	cfg.Finalechat.Enabled = &on
	cfg.Finalechat.BaseURL = fp.srv.URL
	cfg.Finalechat.TokenEnv = "EAGENT_TEST_FC"
	cfg.Finalechat.Artifacts = false
	raw, err := json.Marshal(map[string]any{"finalechat": map[string]any{"artifacts": false}})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(project, ".agents", "eagent")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	return cfg
}

// phoneQuestionTrace is what a question test needs on failure: how each
// question and answer went through the session, what the fake phone holds,
// and the runtime's log lines.
func phoneQuestionTrace(rt *Runtime, fp *phoneFake, ui *fakeUI) string {
	var b strings.Builder
	evs, _ := store.Read(rt.sess.Path)
	for _, ev := range evs {
		switch ev.Type {
		case event.Question, event.UserAnswer, event.UserMessage, event.PhoneQuestion:
			fmt.Fprintf(&b, "event %d %s %s %s\n", ev.Seq, ev.Time.Format("15:04:05.000"), ev.Type, ev.Data)
		}
	}
	fp.mu.Lock()
	for id, q := range fp.questions {
		fmt.Fprintf(&b, "phone question %s status=%v meta=%v\n", id, q["status"], q["meta"])
	}
	fmt.Fprintf(&b, "phone cancelled %v\n", fp.cancelled)
	fp.mu.Unlock()
	ui.mu.Lock()
	for _, l := range ui.logs {
		fmt.Fprintf(&b, "log %s\n", l)
	}
	ui.mu.Unlock()
	return b.String()
}

// tokenUserMessage is what another agent (or our own mirror) posting as the
// user looks like: sender user, origin token. The runtime must ignore it.
func (f *phoneFake) tokenUserMessage(text string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messages = append(f.messages, map[string]any{"id": f.nextID(), "thread_id": "t1", "sender": "user", "body": text, "format": "markdown", "importance": "normal", "origin": "token", "created_at": time.Now().UTC().Format(time.RFC3339Nano)})
	f.cond.Broadcast()
}

// dismiss is the user declining every pending question from the app.
func (f *phoneFake) dismiss() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	any := false
	for _, q := range f.questions {
		if q["status"] == "pending" {
			q["status"] = "dismissed"
			q["answered_at"] = time.Now().UTC().Format(time.RFC3339Nano) // as the service stamps a resolution
			q["answered_at"] = time.Now().UTC().Format(time.RFC3339Nano)
			any = true
		}
	}
	f.cond.Broadcast()
	return any
}

func (f *phoneFake) statuses() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.activity...)
}

// shownStatus is the status line the app shows now, as the service keeps
// it: …/activity writes ordered by seq, a post carrying a line setting it,
// an agent's post without one clearing it, the user's words leaving it.
func (f *phoneFake) shownStatus() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.live == nil || !time.Now().Before(f.live.expires) {
		return ""
	}
	return f.live.text
}

func (f *phoneFake) clearedCount() int { f.mu.Lock(); defer f.mu.Unlock(); return f.cleared }

func (f *phoneFake) metaPatches() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.metas...)
}

// expire is the service timing out every pending question, as its sweep
// does once a question's timeout has passed.
func (f *phoneFake) expire() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, q := range f.questions {
		if q["status"] == "pending" {
			q["status"] = "expired"
			q["answered_at"] = time.Now().UTC().Format(time.RFC3339Nano) // as the service stamps a resolution
			n++
		}
	}
	f.cond.Broadcast()
	return n
}

// cards is how many questions the service holds for the thread: one per
// card the app has shown.
func (f *phoneFake) cards() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.questions) }

// injectQuestion plants a pending question the running mirror never asked
// (from an earlier run, say), tagged with the eagent question id it was
// about; answerQuestion answers that one specifically.
func (f *phoneFake) injectQuestion(fid, eagentQID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.questions[fid] = map[string]any{"id": fid, "thread_id": "t1", "prompt": "old question", "options": []any{}, "allow_freeform": true, "multi_select": false, "status": "pending", "answer": nil, "meta": map[string]any{"eagent": "question", "question_id": eagentQID}, "created_at": time.Now().UTC().Format(time.RFC3339Nano), "expires_at": nil}
}

func (f *phoneFake) answerQuestion(fid, label string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	q := f.questions[fid]
	q["status"] = "answered"
	q["answered_at"] = time.Now().UTC().Format(time.RFC3339Nano) // as the service stamps a resolution
	q["answer"] = map[string]any{"selected": []string{label}}
	f.messages = append(f.messages, map[string]any{"id": f.nextID(), "thread_id": "t1", "sender": "user", "origin": "session", "body": label, "format": "text", "importance": "normal", "meta": map[string]any{"kind": "answer", "question_id": fid}, "created_at": time.Now().UTC().Format(time.RFC3339Nano)})
	f.cond.Broadcast()
}

func (f *phoneFake) getCount() int { f.mu.Lock(); defer f.mu.Unlock(); return f.gets }

// postBodies returns the bodies of the messages posted so far.
func (f *phoneFake) postBodies() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any{}, f.posts...)
}

// retitlePatches returns the PATCH bodies the fake phone has seen so far.
func (f *phoneFake) retitlePatches() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any{}, f.patches...)
}

// phoneRig is a session with the phone mirror on: a fake agent, a scripted
// model, the fake Finalechat.
type phoneRig struct {
	t       *testing.T
	rt      *Runtime
	ui      *fakeUI
	seat    *fakeSeat
	model   *scripted
	fp      *phoneFake
	cfg     config.Config
	project string
	cancel  context.CancelFunc
	done    chan int
	started time.Time // when the latest run began
}

// phoneStart starts a session (interactive or not) whose mirror talks to
// fp. tune adjusts the configuration first; fp nil starts one with no
// phone at all.
func phoneStart(t *testing.T, fp *phoneFake, prompt string, interactive bool, brain func(string, []map[string]any) reply, tune func(*config.Config)) *phoneRig {
	t.Helper()
	t.Setenv("EAGENT_TEST_KEY", "x")
	t.Setenv("EAGENT_TEST_FC", "fc_test")
	seat := newFakeSeat("Merged #42 and #44; main is green at c92b5bad.")
	useFakeRelays(t, seat)
	model := newScripted(brain)
	t.Cleanup(model.srv.Close)
	project := t.TempDir()
	var cfg config.Config
	if fp != nil {
		cfg = phoneFakeConfig(t, project, model.srv.URL, fp)
	} else {
		cfg = testConfig(model.srv.URL)
	}
	if tune != nil {
		tune(&cfg)
	}
	g := &phoneRig{t: t, ui: &fakeUI{input: make(chan string, 16)}, seat: seat, model: model, fp: fp, cfg: cfg, project: project}
	rt, err := New(cfg, Options{Project: project, Interactive: interactive, Prompt: prompt}, g.ui)
	if err != nil {
		t.Fatal(err)
	}
	g.run(rt)
	return g
}

// run starts rt in the background.
func (g *phoneRig) run(rt *Runtime) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	g.rt, g.cancel, g.done, g.started = rt, cancel, make(chan int, 1), time.Now()
	done := g.done
	go func() { done <- rt.Run(ctx) }()
	g.t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
		}
	})
}

// resume reopens the session with opts and runs it.
func (g *phoneRig) resume(opts Options) {
	g.t.Helper()
	opts.Project = g.project
	if g.ui == nil {
		g.ui = &fakeUI{input: make(chan string, 16)}
	}
	rt, err := Resume(g.cfg, opts, g.ui, g.rt.sess.Path)
	if err != nil {
		g.t.Fatal(err)
	}
	g.run(rt)
}

// exit waits for the session to end and returns its exit code.
func (g *phoneRig) exit(within time.Duration) int {
	g.t.Helper()
	select {
	case code := <-g.done:
		g.done <- code
		return code
	case <-time.After(within):
		g.t.Fatalf("the session did not end; said %q; logs %v", g.ui.said(), g.ui.logged())
	}
	return -1
}

// running fails the test if the session ends within d.
func (g *phoneRig) running(d time.Duration, why string) {
	g.t.Helper()
	select {
	case code := <-g.done:
		g.done <- code
		g.t.Fatalf("the session ended (%d) %s; logs %v", code, why, g.ui.logged())
	case <-time.After(d):
	}
}

// lastMessageID is the id of the newest message in the thread.
func (f *phoneFake) lastMessageID() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.messages[len(f.messages)-1]["id"].(string)
}

// phoneWeighed waits until the message poll has handled the message id:
// it has asked for what comes after it, which it does only once it is done
// with it, and what it handed the loop has run.
func (g *phoneRig) phoneWeighed(id string) {
	g.t.Helper()
	waitFor(g.t, "the poll to take the message", func() bool {
		g.fp.mu.Lock()
		defer g.fp.mu.Unlock()
		return g.fp.after == id
	})
	g.rt.sync(func() {})
}

// statusIdle waits until the status keeper has nothing armed: its first
// look (statusFirstLook after it starts) is past, the line on the phone is what
// the session wants, and more than its 200ms floor between looks has
// passed since, so any wake it had set has come and gone. A change after
// this reaches the phone only if it wakes the keeper.
func (g *phoneRig) statusIdle() {
	g.t.Helper()
	caughtUp := func() bool {
		var p *phone
		g.rt.sync(func() { p = g.rt.phone })
		if p == nil {
			return false
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.want.Text == p.sent.Text && p.want.Kind == p.sent.Kind
	}
	waitFor(g.t, "the status keeper to be idle", func() bool {
		if time.Since(g.started) < statusFirstLook+300*time.Millisecond || !caughtUp() {
			return false
		}
		time.Sleep(300 * time.Millisecond)
		return caughtUp()
	})
}

// interrupt cancels the run and waits for it.
func (g *phoneRig) interrupt() {
	g.t.Helper()
	g.cancel()
	g.exit(20 * time.Second)
}

// log reads the session's record from disk.
func (g *phoneRig) log() []event.Event {
	evs, _ := store.Read(g.rt.sess.Path)
	return evs
}

// phoneAskAndStop is the reply that asks the user a question and stops the
// agent: a batch session then has nothing left but the question.
func phoneAskAndStop(question string, options ...string) reply {
	args, _ := json.Marshal(map[string]any{"text": question, "options": options})
	return reply{calls: []event.ToolCall{tc("ask_user", string(args)), tc("relay_control", `{"action":"stop","reason":"waiting on the user"}`)}}
}

// phoneEnding reports a call made for the closing word of a session with
// no terminal.
func phoneEnding(msgs []map[string]any) bool {
	return strings.Contains(lastUserText(msgs), "The session is ending now")
}

// phoneLastIsTool reports a call that follows tool results in the same
// turn.
func phoneLastIsTool(msgs []map[string]any) bool { return msgs[len(msgs)-1]["role"] == "tool" }

// phoneCopy is a shallow copy of a question, taken under the fake's lock so
// encoding it later cannot race a change (the old fake encoded the live
// map after unlocking).
func phoneCopy(q map[string]any) map[string]any {
	if q == nil {
		return nil
	}
	out := make(map[string]any, len(q))
	for k, v := range q {
		out[k] = v
	}
	return out
}

// agentStopped waits until no agent is active: a script's stop has run.
func (g *phoneRig) agentStopped() {
	g.t.Helper()
	waitFor(g.t, "the agent to be stopped", func() bool {
		n := 1
		g.rt.sync(func() { n = len(g.rt.st.ActiveRelays()) })
		return n == 0
	})
}

// phoneAfterLook reports a call that follows a tool's result, or the
// picture view_image brought back, in the same turn.
func phoneAfterLook(msgs []map[string]any) bool {
	return phoneLastIsTool(msgs) || strings.Contains(lastUserText(msgs), "image from view_image")
}

var _ = os.Getenv
var _ = filepath.Join
