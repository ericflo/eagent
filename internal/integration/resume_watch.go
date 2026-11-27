package integration

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/filelock"
	"github.com/ericflo/eagent/internal/finalechat"
	"github.com/ericflo/eagent/internal/store"
)

// PhoneMessage is a reply the user sent from the app to the thread of a
// session that was not running.
type PhoneMessage struct {
	ID          string
	Text        string
	Attachments []event.Attachment
	// Meta is the message's own meta, where the app puts the composer's
	// route and delivery and the client's capabilities; the session that
	// takes the message reads them there, as it does when it is running.
	Meta map[string]any
}

// ResumeHost is a long-lived process that can reopen finished sessions.
type ResumeHost interface {
	// Hosted reports whether this process is running the session.
	Hosted(sessionID string) bool
	// Resume reopens the session with the reply as its next turn.
	Resume(sessionID string, msg PhoneMessage) error
}

// StartResumeWatcher brings finished sessions back to life when the user
// writes to their threads from the phone, the way writing to them in the web
// UI does. One process per project holds the watch; it follows the
// account's event stream and, on connect and every few minutes, sweeps for
// replies that arrived while nothing was watching. Sessions that are
// running anywhere are left to their own mirror.
func StartResumeWatcher(ctx context.Context, project string, host ResumeHost, logf func(string, ...any)) func() {
	if finalechat.Disabled() {
		return func() {}
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	retries := &replyRetries{replies: map[string]pendingReply{}}
	go func() {
		defer close(done)
		lastError := ""
		for ctx.Err() == nil {
			lockCtx, stop := context.WithTimeout(ctx, 100*time.Millisecond)
			unlock, err := filelock.Acquire(lockCtx, filepath.Join(stateDir(project), "resume-watch.lock"))
			stop()
			if err == nil {
				err = watchResumes(ctx, project, host, retries, logf)
				unlock()
				if err != nil && ctx.Err() == nil && err.Error() != lastError {
					lastError = err.Error()
					logf("finalechat: reply watch: %v (will retry)", err)
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
	}()
	return func() { cancel(); <-done; retries.writeDown(project, logf) }
}

// keptFile is where a stopping reply watcher writes down the replies it
// keeps (quietly, keepUntilStopped, or told it keeps trying, retryLater),
// and the sweep of whoever holds the watch next takes them up, once: a
// session that stops meanwhile hides its thread from the sweep behind its
// closing note, as eagent's note that it keeps trying does. A hard kill
// writes nothing, as for the serve's list of hosted sessions.
func keptFile(project string) string {
	return filepath.Join(stateDir(project), "resume-watch-kept.json")
}

// keptReply is a reply written down, by its id, with the time it was first
// kept: the day's cutoff (retryPending) counts from then, across restarts.
type keptReply struct {
	Thread  string             `json:"thread"`
	Message finalechat.Message `json:"message"`
	Since   time.Time          `json:"since"`
	Told    bool               `json:"told,omitempty"`
}

// lockKept holds the kept file for one process's read and write of it: two
// watchers (the serve's and `connector run`'s) may stop, or sweep, at once.
func lockKept(project string) (func(), error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return filelock.Acquire(ctx, keptFile(project)+".lock")
}

// writeDown adds the replies kept to those written down that nobody has
// taken up yet.
func (r *replyRetries) writeDown(project string, logf func(string, ...any)) {
	unlock, err := lockKept(project)
	if err != nil {
		logf("finalechat: could not note the replies kept for the next start: %v", err)
		return
	}
	defer unlock()
	kept := map[string]keptReply{}
	if raw, err := os.ReadFile(keptFile(project)); err == nil {
		_ = json.Unmarshal(raw, &kept)
	}
	r.mu.Lock()
	for id, p := range r.replies {
		kept[id] = keptReply{Thread: p.thread.ExternalID, Message: p.msg, Since: p.since, Told: p.told}
	}
	r.mu.Unlock()
	if len(kept) == 0 {
		return
	}
	if err := writeJSONAtomic(keptFile(project), kept); err != nil {
		logf("finalechat: could not note the replies kept for the next start: %v", err)
	}
}

// takeUp keeps what an earlier watcher wrote down, as it kept it: a reply
// the thread was told about is not told about again (retryLater).
func (r *replyRetries) takeUp(project string) {
	if _, err := os.Stat(keptFile(project)); err != nil {
		return
	}
	unlock, err := lockKept(project)
	if err != nil {
		return // the next sweep takes it up
	}
	defer unlock()
	raw, err := os.ReadFile(keptFile(project))
	if err != nil {
		return
	}
	_ = os.Remove(keptFile(project))
	var kept map[string]keptReply
	_ = json.Unmarshal(raw, &kept)
	for id, k := range kept {
		thread := finalechat.Thread{ExternalID: k.Thread}
		if !k.Told {
			r.keepQuietly(thread, k.Message, k.Since)
			continue
		}
		r.mu.Lock()
		if _, ok := r.replies[id]; !ok {
			r.replies[id] = pendingReply{thread: thread, msg: k.Message, since: k.Since, told: true}
		}
		r.mu.Unlock()
	}
}

type resumeWatcher struct {
	project string
	host    ResumeHost
	client  *finalechat.Client
	logf    func(string, ...any)
	retries *replyRetries
	mu      sync.Mutex
	handled map[string]time.Time // message ids acted on, or being acted on
}

// pendingReply is a reply whose session could not be resumed yet, or one
// that reached a running session (told false: keepUntilStopped).
type pendingReply struct {
	thread finalechat.Thread
	msg    finalechat.Message
	since  time.Time
	told   bool
}

// replyRetries outlives one connection to the event stream: a reply that
// could not resume its session is tried again every sweep, for a day.
type replyRetries struct {
	mu      sync.Mutex
	replies map[string]pendingReply
}

// leaveWithWatcher writes m down, quietly, for the next sweep of whoever
// holds the project's watch (this process or another), as a stopping
// watcher writes down what it keeps.
func leaveWithWatcher(project string, info store.Info, m finalechat.Message) {
	r := &replyRetries{replies: map[string]pendingReply{m.ID: {thread: finalechat.Thread{ExternalID: "eagent:" + info.ID}, msg: m, since: time.Now()}}}
	r.writeDown(project, func(string, ...any) {})
}

// watchResumes runs one connection to the event stream, sweeping first and
// then every minute while it lasts, since the stream has no replay and a
// busy server may drop an event for a subscriber.
func watchResumes(ctx context.Context, project string, host ResumeHost, retries *replyRetries, logf func(string, ...any)) error {
	client, err := settingsAccount(project)
	if err != nil {
		return err
	}
	client.UserAgent = "eagent-resume/1"
	w := &resumeWatcher{project: project, host: host, client: client, logf: logf, retries: retries, handled: map[string]time.Time{}}
	w.sweep(ctx)
	sweepCtx, stopSweeps := context.WithCancel(ctx)
	defer stopSweeps()
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-sweepCtx.Done():
				return
			case <-ticker.C:
				w.sweep(sweepCtx)
			}
		}
	}()
	return client.Events(ctx, func(name string, data []byte) error {
		if name != "message.created" {
			return nil
		}
		var ev struct {
			Thread  finalechat.Thread  `json:"thread"`
			Message finalechat.Message `json:"message"`
		}
		if err := json.Unmarshal(data, &ev); err != nil {
			logf("finalechat: reply watch: unreadable message event: %v", err)
			return nil
		}
		w.consider(ctx, ev.Thread, ev.Message)
		return nil
	})
}

// consider resumes the thread's session when it is one of this project's,
// nobody is running it, and the user wrote to it from the app since it
// last took a message in. trigger is the message that brought the thread
// up (from the event stream, or a retry); a sweep passes none. The session
// is resumed with the earliest message it has not taken in, whatever
// triggered it (nextPhoneMessage); its poll then takes the rest in order.
func (w *resumeWatcher) consider(ctx context.Context, thread finalechat.Thread, trigger finalechat.Message) {
	if trigger.ID != "" && (trigger.Sender != "user" || trigger.Origin == "token" || trigger.Deleted) {
		return
	}
	sid, ok := strings.CutPrefix(thread.ExternalID, "eagent:")
	if !ok {
		return
	}
	info, ok := w.session(sid)
	if !ok {
		return // not a session of this project
	}
	if store.Alive(info.Path) || w.host.Hosted(sid) {
		if trigger.ID != "" {
			// Said for a reply seen, not for each sweep's look at the
			// one kept quietly for when the session stops.
			w.logf("finalechat: reply to session %s left to its running mirror", sid)
			w.keepUntilStopped(info, thread, trigger)
		}
		return
	}
	var held *finalechat.Message
	if trigger.ID != "" {
		held = &trigger
	}
	m, found, err := nextPhoneMessage(ctx, w.client, info, held)
	if err != nil {
		w.logf("finalechat: could not read the thread of session %s: %v (will retry)", sid, err)
		return // a reply kept for retry stays kept
	}
	if !found {
		if trigger.ID != "" {
			w.retries.mu.Lock()
			delete(w.retries.replies, trigger.ID)
			w.retries.mu.Unlock()
		}
		// The session stopped and took in all there is: a reply kept
		// while it ran (keepUntilStopped) has had its look.
		w.dropKept(thread.ExternalID)
		return
	}
	w.mu.Lock()
	for id, at := range w.handled {
		if time.Since(at) > time.Hour {
			delete(w.handled, id)
		}
	}
	if _, seen := w.handled[m.ID]; seen {
		w.mu.Unlock()
		return
	}
	// Claimed while it is acted on. The claim stands only once the reply is
	// dealt with; a resume that failed is tried again by the next sweep
	// instead of being dropped until eagent restarts.
	w.handled[m.ID] = time.Now()
	w.mu.Unlock()
	settled := false
	defer func() {
		if !settled {
			w.mu.Lock()
			delete(w.handled, m.ID)
			w.mu.Unlock()
		}
	}()
	msg := PhoneMessage{ID: m.ID, Text: strings.TrimSpace(m.Body), Meta: m.Meta}
	failed := make([]error, len(m.Attachments))
	// The message's files, tries again included, get the time one
	// download gets: a reply from the event stream holds it no longer.
	fetchCtx, stopFetch := context.WithTimeout(ctx, fetchBudget)
	defer stopFetch()
	for i, a := range m.Attachments {
		saved, err := saveAttachment(fetchCtx, w.client, info.Path, a)
		// A download that may work later (the service could not be
		// reached, or answered 429 or 5xx) is tried three times, as the
		// client tries a post, before the file is refused.
		for try := 1; try < 3 && err != nil && fetchCtx.Err() == nil && finalechat.Transient(err); try++ {
			select {
			case <-fetchCtx.Done():
			case <-time.After(time.Duration(try) * fetchRetryStep):
				saved, err = saveAttachment(fetchCtx, w.client, info.Path, a)
			}
		}
		if err != nil {
			w.logf("finalechat: could not fetch %s for session %s: %v", a.Filename, sid, err)
			if errors.Is(ctx.Err(), context.Canceled) {
				// The watcher is stopping (the serve is), which cut the
				// fetch off: nobody is told anything, about this file or
				// one before it, and nothing is taken, so the reply is
				// still the thread's latest message; the next sweep, or
				// the next serve, reads it.
				return
			}
			failed[i] = err
			continue
		}
		msg.Attachments = append(msg.Attachments, saved)
	}
	for i, err := range failed {
		if err == nil {
			continue
		}
		// As the running mirror does: the sender is told, and the
		// session comes back with the rest of the message. The note
		// gets its own time, as the fetch may have used up the
		// sweep's. When it cannot be posted nobody would know: the
		// reply is not taken in, and is kept, quietly, for the sweeps
		// to read the thread for (an earlier notice may have made
		// eagent's the thread's latest message).
		if tellRefused(context.WithoutCancel(ctx), w.client, thread.ExternalID, m.ID, m.Attachments[i], err) != nil {
			w.retries.mu.Lock()
			if _, kept := w.retries.replies[m.ID]; !kept {
				w.retries.replies[m.ID] = pendingReply{thread: thread, msg: m, since: time.Now()}
			}
			w.retries.mu.Unlock()
			return
		}
	}
	// A message whose files were all refused (the person was told) still
	// resumes the session: it takes the message in, so the ones after it
	// are read in order instead of waiting behind it for ever.
	err = w.host.Resume(sid, msg)
	if trigger.ID != "" && trigger.ID != m.ID {
		// The trigger is read by the session's poll, after m; when the
		// resume failed, m is kept in its stead (retryLater).
		w.retries.mu.Lock()
		delete(w.retries.replies, trigger.ID)
		w.retries.mu.Unlock()
	}
	if err != nil {
		w.logf("finalechat: could not resume session %s for your reply: %v (will retry)", sid, err)
		w.retryLater(ctx, thread, m, "eagent could not resume this session for your reply yet: "+err.Error()+". It keeps trying every minute.")
		return
	}
	settled = true
	w.retries.mu.Lock()
	delete(w.retries.replies, m.ID)
	w.retries.mu.Unlock()
	// What the session does not take in before it stops again (a /quit it
	// was resumed with ends it before it reads on; a reply kept while it
	// came back) is looked for once it has stopped, as for any reply that
	// reached it while it ran: its closing note may hide the thread from
	// the sweep.
	w.keepUntilStopped(info, thread, m)
	w.logf("finalechat: session %s resumed with your reply from the phone", sid)
}

// fetchRetryStep is the wait before the second try of a download, twice it
// before the third; a variable so tests can shorten it.
var fetchRetryStep = time.Second

// fetchBudget bounds one message's downloads: the limit of one download
// (finalechat's Download); a variable so tests can shorten it.
var fetchBudget = 120 * time.Second

// keepUntilStopped keeps, quietly, a reply that reached a running session,
// for the sweeps to look at once it has stopped: a session shutting down
// may drop it, and its closing note then hides the thread from the sweep.
// The session's ledger says then whether it took the reply in
// (nextPhoneMessage), so only a run that keeps the ledger has it kept. One
// per thread is enough: the session is resumed with the earliest message
// it has not taken in, whichever reply brought it up.
func (w *resumeWatcher) keepUntilStopped(info store.Info, thread finalechat.Thread, m finalechat.Message) {
	w.retries.mu.Lock()
	_, kept := w.retries.replies[m.ID]
	w.retries.mu.Unlock()
	if kept {
		return // being retried, or sent twice by the stream
	}
	evs, err := store.Read(info.Path)
	if err != nil || !ReadPhoneLedger(evs).Keeps {
		return
	}
	w.retries.keepQuietly(thread, m, time.Now())
}

// keepQuietly keeps m as the thread's one quiet entry (keepUntilStopped),
// kept since then.
func (r *replyRetries) keepQuietly(thread finalechat.Thread, m finalechat.Message, since time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, p := range r.replies {
		if !p.told && p.thread.ExternalID == thread.ExternalID {
			delete(r.replies, id)
		}
	}
	if _, kept := r.replies[m.ID]; !kept {
		r.replies[m.ID] = pendingReply{thread: thread, msg: m, since: since}
	}
}

// dropKept forgets the replies keepUntilStopped kept for a thread.
func (w *resumeWatcher) dropKept(externalID string) {
	w.retries.mu.Lock()
	defer w.retries.mu.Unlock()
	for id, p := range w.retries.replies {
		if !p.told && p.thread.ExternalID == externalID {
			delete(w.retries.replies, id)
		}
	}
}

// retryLater keeps a reply that could not resume its session for the next
// sweeps, and says so in the thread once, so it does not look ignored.
func (w *resumeWatcher) retryLater(ctx context.Context, thread finalechat.Thread, m finalechat.Message, note string) {
	w.retries.mu.Lock()
	p, ok := w.retries.replies[m.ID]
	if !ok {
		p = pendingReply{thread: thread, msg: m, since: time.Now()}
	}
	tell := !p.told
	p.told = true
	w.retries.replies[m.ID] = p
	w.retries.mu.Unlock()
	if tell {
		postCtx, stop := context.WithTimeout(ctx, 30*time.Second)
		defer stop()
		_, _, _ = w.client.Post(postCtx, finalechat.Ref(thread.ExternalID), finalechat.PostRequest{Body: note, Sender: "system", Format: "text", ClientKey: "resume-failed-" + m.ID})
	}
}

// retryPending tries the replies that could not resume their sessions
// before. A day after the first try a reply is let go.
func (w *resumeWatcher) retryPending(ctx context.Context) {
	w.retries.mu.Lock()
	pending := make([]pendingReply, 0, len(w.retries.replies))
	for id, p := range w.retries.replies {
		if time.Since(p.since) > 24*time.Hour {
			delete(w.retries.replies, id)
			continue
		}
		pending = append(pending, p)
	}
	w.retries.mu.Unlock()
	for _, p := range pending {
		if ctx.Err() != nil {
			return
		}
		if !p.told {
			// Kept while its session ran: the thread is read as a sweep
			// reads it, not from the copy kept, which knows nothing of a
			// deletion since.
			w.consider(ctx, p.thread, finalechat.Message{})
			continue
		}
		w.consider(ctx, p.thread, p.msg)
	}
}

func (w *resumeWatcher) session(id string) (store.Info, bool) {
	infos, err := store.List(store.Root(w.project))
	if err != nil {
		return store.Info{}, false
	}
	for _, info := range infos {
		if info.ID == id {
			return info, true
		}
	}
	return store.Info{}, false
}

// sweep finds threads whose latest message is the user's and newer than the
// session's last log write: a reply nothing was watching for. Which message
// to resume with is nextPhoneMessage's to say; a message the session never
// took in that is older than its last write is still found then, first,
// once the person writes again or the session is resumed from elsewhere.
func (w *resumeWatcher) sweep(ctx context.Context) {
	var page struct {
		Threads []struct {
			ID             string     `json:"id"`
			ExternalID     string     `json:"external_id"`
			PreviewSender  string     `json:"preview_sender"`
			LastActivityAt time.Time  `json:"last_activity_at"`
			ArchivedAt     *time.Time `json:"archived_at"`
		} `json:"threads"`
		NextCursor string `json:"next_cursor"`
	}
	sweepCtx, stop := context.WithTimeout(ctx, 60*time.Second)
	defer stop()
	w.retries.takeUp(w.project)
	w.retryPending(sweepCtx)
	// The list is newest first: it is read on, page after page, while a
	// thread in it can be newer than the oldest last write of the
	// project's sessions.
	oldest := time.Now()
	if infos, err := store.List(store.Root(w.project)); err == nil {
		for _, info := range infos {
			if info.Modified.Before(oldest) {
				oldest = info.Modified
			}
		}
	}
	q := url.Values{"limit": {"200"}}
	for {
		page.Threads, page.NextCursor = nil, ""
		if err := w.client.Request(sweepCtx, "GET", "/api/v1/threads", q, nil, &page, 0); err != nil {
			return
		}
		for _, t := range page.Threads {
			if sweepCtx.Err() != nil {
				return
			}
			sid, ok := strings.CutPrefix(t.ExternalID, "eagent:")
			if !ok || t.PreviewSender != "user" || t.ArchivedAt != nil {
				continue
			}
			info, ok := w.session(sid)
			if !ok || store.Alive(info.Path) || w.host.Hosted(sid) {
				continue
			}
			if !t.LastActivityAt.After(info.Modified) {
				continue
			}
			w.consider(sweepCtx, finalechat.Thread{ID: t.ID, ExternalID: t.ExternalID}, finalechat.Message{})
		}
		if page.NextCursor == "" || len(page.Threads) == 0 || !page.Threads[len(page.Threads)-1].LastActivityAt.After(oldest) {
			return
		}
		q.Set("cursor", page.NextCursor)
	}
}

// PendingReply is the earliest message in a session's thread that the user
// sent from the app and the session has not taken in (nextPhoneMessage, as
// the reply watcher picks it): a reply sent while nothing ran the session.
// Whoever resumes the session passes it as the prompt; the session's poll
// reads on after it and takes the rest in order. ok is false when there is
// none, or no phone; err when the thread could not be read.
func PendingReply(ctx context.Context, project string, info store.Info) (PhoneMessage, bool, error) {
	if finalechat.Disabled() {
		return PhoneMessage{}, false, nil
	}
	client, err := settingsAccount(project)
	if err != nil {
		return PhoneMessage{}, false, nil // no phone account: nothing can have been sent
	}
	last, ok, err := nextPhoneMessage(ctx, client, info, nil)
	if err != nil {
		return PhoneMessage{}, false, err
	}
	if !ok {
		return PhoneMessage{}, false, nil
	}
	msg := PhoneMessage{ID: last.ID, Text: strings.TrimSpace(last.Body), Meta: last.Meta}
	var big []finalechat.Attachment
	for _, a := range last.Attachments {
		saved, err := saveAttachment(ctx, client, info.Path, a)
		if errors.Is(err, finalechat.ErrTooLarge) {
			big = append(big, a)
			continue
		}
		if err != nil {
			return PhoneMessage{}, false, err
		}
		msg.Attachments = append(msg.Attachments, saved)
	}
	// A file too large will never fit: the session comes back with the
	// rest of the message, and the sender is told about the file, once
	// every other file came (a failed download posts nothing, so the
	// thread's latest message is still the reply for the watcher's sweep).
	// When a notice cannot be posted, nothing is taken, as for a failed
	// download.
	for i, a := range big {
		if err := tellRefused(ctx, client, "eagent:"+info.ID, last.ID, a, finalechat.ErrTooLarge); err != nil {
			if i > 0 {
				// A notice before it went: eagent's is the thread's latest
				// message, which hides the reply from the sweep. The
				// project's reply watcher keeps it quietly, as it keeps
				// one whose notice it could not post itself.
				leaveWithWatcher(project, info, last)
			}
			return PhoneMessage{}, false, err
		}
	}
	// What the session resumed with it does not take in before it stops
	// again (a /quit ends it before it reads on) is looked for by the
	// project's reply watcher once it has stopped, as after a resume of the
	// watcher's own: its closing note may hide the thread from the sweep.
	leaveWithWatcher(project, info, last)
	// Also a message whose files were all refused: the session takes it
	// in, so the ones after it are read in order.
	return msg, true, nil
}

// tellRefused posts, in the thread, why a file the user sent was not
// taken. The key, the running mirror's for the same notice, makes it once
// per file whoever notices first.
func tellRefused(ctx context.Context, client *finalechat.Client, externalID, msgID string, a finalechat.Attachment, err error) error {
	postCtx, stop := context.WithTimeout(ctx, 30*time.Second)
	defer stop()
	_, _, err = client.Post(postCtx, finalechat.Ref(externalID), finalechat.PostRequest{Body: finalechat.RefusedNote(a.Filename, err), Sender: "system", Format: "text", Meta: map[string]any{"eagent": "notice", "kind": "attachment_refused"}, ClientKey: externalID + ":refused:" + msgID + ":" + a.ID})
	return err
}

// saveAttachment downloads a phone attachment under the session directory,
// named the way the running mirror names them (finalechat.InboundFileName).
func saveAttachment(ctx context.Context, client *finalechat.Client, sessionPath string, a finalechat.Attachment) (event.Attachment, error) {
	dir := filepath.Join(sessionPath, "attachments")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return event.Attachment{}, err
	}
	if a.Size > finalechat.MaxAttachmentSize {
		return event.Attachment{}, finalechat.ErrTooLarge
	}
	file, name := finalechat.InboundFileName(a.ID, a.Filename)
	dst := filepath.Join(dir, file)
	f, err := os.Create(dst)
	if err != nil {
		return event.Attachment{}, err
	}
	ct, _, err := client.Download(ctx, a.URL, f)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(dst)
		return event.Attachment{}, err
	}
	st, err := os.Stat(dst)
	if err != nil {
		return event.Attachment{}, err
	}
	if st.Size() == 0 {
		_ = os.Remove(dst)
		return event.Attachment{}, errors.New("empty download")
	}
	if a.ContentType != "" {
		ct = a.ContentType
	}
	if i := strings.IndexByte(ct, ';'); i > 0 {
		ct = ct[:i]
	}
	kind := a.Kind
	if kind == "" {
		if strings.HasPrefix(ct, "image/") {
			kind = "image"
		} else {
			kind = "file"
		}
	}
	return event.Attachment{Path: dst, Name: name, ContentType: ct, Size: st.Size(), Kind: kind, Width: a.Width, Height: a.Height, ID: a.ID}, nil
}
