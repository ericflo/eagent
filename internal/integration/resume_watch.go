package integration

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
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
	go func() {
		defer close(done)
		lastError := ""
		for ctx.Err() == nil {
			lockCtx, stop := context.WithTimeout(ctx, 100*time.Millisecond)
			unlock, err := filelock.Acquire(lockCtx, filepath.Join(stateDir(project), "resume-watch.lock"))
			stop()
			if err == nil {
				err = watchResumes(ctx, project, host, logf)
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
	return func() { cancel(); <-done }
}

type resumeWatcher struct {
	project string
	host    ResumeHost
	client  *finalechat.Client
	logf    func(string, ...any)
	mu      sync.Mutex
	handled map[string]time.Time // message ids already acted on
}

// watchResumes runs one connection to the event stream, sweeping first and
// then every minute while it lasts, since the stream has no replay and a
// busy server may drop an event for a subscriber.
func watchResumes(ctx context.Context, project string, host ResumeHost, logf func(string, ...any)) error {
	client, err := settingsAccount(project)
	if err != nil {
		return err
	}
	client.UserAgent = "eagent-resume/1"
	w := &resumeWatcher{project: project, host: host, client: client, logf: logf, handled: map[string]time.Time{}}
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

// consider resumes the message's session when it is one of this project's,
// nobody is running it, and the message is the user's own from the app.
func (w *resumeWatcher) consider(ctx context.Context, thread finalechat.Thread, m finalechat.Message) {
	if m.Sender != "user" || m.Origin == "token" || m.Deleted || m.ID == "" {
		return
	}
	sid, ok := strings.CutPrefix(thread.ExternalID, "eagent:")
	if !ok {
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
	w.handled[m.ID] = time.Now()
	w.mu.Unlock()
	info, ok := w.session(sid)
	if !ok {
		return // not a session of this project
	}
	if store.Alive(info.Path) || w.host.Hosted(sid) {
		w.logf("finalechat: reply to session %s left to its running mirror", sid)
		return
	}
	msg := PhoneMessage{ID: m.ID, Text: strings.TrimSpace(m.Body)}
	for _, a := range m.Attachments {
		saved, err := saveAttachment(ctx, w.client, info.Path, a)
		if err != nil {
			w.logf("finalechat: could not fetch %s for session %s: %v", a.Filename, sid, err)
			continue
		}
		msg.Attachments = append(msg.Attachments, saved)
	}
	if msg.Text == "" && len(msg.Attachments) == 0 {
		return
	}
	if err := w.host.Resume(sid, msg); err != nil {
		w.logf("finalechat: could not resume session %s for your reply: %v", sid, err)
		return
	}
	w.logf("finalechat: session %s resumed with your reply from the phone", sid)
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
// session's last log write: a reply nothing was watching for.
func (w *resumeWatcher) sweep(ctx context.Context) {
	var page struct {
		Threads []struct {
			ID             string     `json:"id"`
			ExternalID     string     `json:"external_id"`
			PreviewSender  string     `json:"preview_sender"`
			LastActivityAt time.Time  `json:"last_activity_at"`
			ArchivedAt     *time.Time `json:"archived_at"`
		} `json:"threads"`
	}
	sweepCtx, stop := context.WithTimeout(ctx, 60*time.Second)
	defer stop()
	if err := w.client.Request(sweepCtx, "GET", "/api/v1/threads", url.Values{"limit": {"200"}}, nil, &page, 0); err != nil {
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
		if !ok || !t.LastActivityAt.After(info.Modified) || store.Alive(info.Path) || w.host.Hosted(sid) {
			continue
		}
		last, ok := w.lastUserMessage(sweepCtx, t.ExternalID)
		if !ok || !last.CreatedAt.After(info.Modified) {
			continue
		}
		w.consider(sweepCtx, finalechat.Thread{ID: t.ID, ExternalID: t.ExternalID}, last)
	}
}

// lastUserMessage pages through the user's messages in a thread and returns
// the newest one the user wrote from the app.
func (w *resumeWatcher) lastUserMessage(ctx context.Context, external string) (finalechat.Message, bool) {
	var last finalechat.Message
	found := false
	after := ""
	for i := 0; i < 50; i++ {
		msgs, _, _, err := w.client.MessagesPage(ctx, finalechat.Ref(external), after, "user", 0, 100)
		if err != nil || len(msgs) == 0 {
			break
		}
		for _, m := range msgs {
			after = m.ID
			if m.Sender == "user" && m.Origin != "token" && !m.Deleted {
				last, found = m, true
			}
		}
		if len(msgs) < 100 {
			break
		}
	}
	return last, found
}

var unsafeAttachmentName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// saveAttachment downloads a phone attachment under the session directory,
// named the way the running mirror names them.
func saveAttachment(ctx context.Context, client *finalechat.Client, sessionPath string, a finalechat.Attachment) (event.Attachment, error) {
	dir := filepath.Join(sessionPath, "attachments")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return event.Attachment{}, err
	}
	short := a.ID
	if len(short) > 8 {
		short = short[:8]
	}
	name := strings.Trim(unsafeAttachmentName.ReplaceAllString(filepath.Base(strings.TrimSpace(a.Filename)), "_"), "._")
	if name == "" {
		name = "file"
	}
	dst := filepath.Join(dir, "in-"+short+"-"+name)
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
