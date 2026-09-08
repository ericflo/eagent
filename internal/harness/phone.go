package harness

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/finalechat"
)

// The phone mirror keeps a Finalechat thread in step with the session: the
// narrator's messages and questions go to the user's phone, and what the
// user taps or types there comes back as user messages and answers, through
// the same path the terminal and web UI use. Everything the phone does is
// recorded in the session log, so replay and the web UI see it too.
//
// Outbound calls run on one worker goroutine in order, off the loop. Inbound
// replies are long-polled on a second goroutine and handed to the loop.

// phone is the loop-side handle.
type phone struct {
	client  *finalechat.Client
	ref     string // ext:<external id>
	title   string
	agent   string
	timeout int  // question lifetime in seconds
	mirror  bool // copy terminal and web input to the thread
	remote  bool // the user said they are away from the terminal

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	queue  chan func(context.Context)

	sid     string // the eagent session id
	resumes int    // how many times the session had been resumed when the mirror started

	// The status line (see phonestatus.go): want is what the loop last
	// derived, sent is what the server shows, stats feed the thread meta.
	features map[string]bool
	want     finalechat.Activity
	sent     finalechat.Activity
	sentAt   time.Time
	stats    phoneStats
	wake     chan struct{}

	mu         sync.Mutex
	posted     map[string]bool   // message ids we created (skipped on poll)
	questions  map[string]string // eagent question id -> finalechat question id
	fromPhone  map[string]string // finalechat question id -> eagent question id
	lastID     string            // newest message id seen, for the poll
	gone       map[string]bool   // eagent question ids cancelled or expired on the phone
	threadID   string
	threadSeen bool
}

// startPhone wires the mirror up if a token is available and it is not
// disabled. Called after the session's opening events are recorded.
func (r *Runtime) startPhone() {
	fc := r.cfg.Finalechat
	if !fc.Wanted() {
		return
	}
	switch strings.ToLower(os.Getenv("EAGENT_FINALECHAT")) {
	case "0", "off", "false", "no":
		return // the environment kill switch wins over any configuration
	}
	client, ok := finalechat.Resolve(fc.TokenEnvName(), fc.BaseURL)
	if !ok {
		if fc.Required() {
			r.ui.Log("finalechat: enabled in config but no token in $%s or ~/.config/finalechat/config.json", fc.TokenEnvName())
		}
		return
	}
	client.UserAgent = "eagent/" + Version
	ctx, cancel := context.WithCancel(context.Background())
	p := &phone{
		client: client, ref: finalechat.Ref("eagent:" + r.sess.ID),
		title: filepath.Base(r.opts.Project), agent: fc.AgentName(),
		timeout: fc.QuestionTimeout(), mirror: fc.Mirror(),
		sid: r.sess.ID, resumes: r.st.Resumes, features: map[string]bool{}, wake: make(chan struct{}, 1),
		ctx: ctx, cancel: cancel, queue: make(chan func(context.Context), 256),
		posted: map[string]bool{}, questions: map[string]string{}, fromPhone: map[string]string{}, gone: map[string]bool{},
	}
	r.phone = p
	p.wg.Add(1)
	go p.worker()

	resumed := r.st.Resumes > 0
	var pending *event.NarratorQuestionData
	if q := r.st.Question; q != nil {
		pending = &event.NarratorQuestionData{ID: q.ID, Text: q.Text, Options: q.Options}
	}
	// The message the session was started or resumed with. It was appended
	// before the mirror existed, so observe() never saw it; it is posted
	// here, as the user, so the phone shows the conversation whole.
	prompt, promptSeq := "", int64(0)
	if !resumed {
		for _, ev := range r.st.Events {
			if ev.Type == event.UserMessage {
				var d event.UserMessageData
				_ = ev.Decode(&d)
				prompt, promptSeq = d.Text, ev.Seq
				break
			}
		}
	} else {
		var resumeSeq int64
		for _, ev := range r.st.Events {
			switch ev.Type {
			case event.SessionResume:
				resumeSeq = ev.Seq
				prompt, promptSeq = "", 0
			case event.UserMessage:
				if ev.Seq > resumeSeq && resumeSeq > 0 {
					var d event.UserMessageData
					_ = ev.Decode(&d)
					if d.Source != "finalechat" {
						prompt, promptSeq = d.Text, ev.Seq
					}
				}
			case event.UserAnswer:
				if ev.Seq > resumeSeq && resumeSeq > 0 {
					var d event.UserAnswerData
					_ = ev.Decode(&d)
					if d.Source != "finalechat" {
						prompt, promptSeq = d.Text, ev.Seq
					}
				}
			}
		}
	}
	models := map[string]string{}
	for k, v := range r.st.Models {
		models[k] = v
	}
	// The first call proves the token and learns the account's settings,
	// creates the thread, and anchors the reply poll on a message we own.
	p.enqueue(func(ctx context.Context) {
		me, err := p.client.Me(ctx)
		if err != nil {
			r.ui.Log("finalechat: %v; the phone mirror is off for this session", err)
			r.post(func() { r.phone = nil })
			p.cancel()
			return
		}
		p.mu.Lock()
		p.remote = me.User.Settings.RemoteMode
		p.features = featureSet(me.Features)
		p.mu.Unlock()
		body := "eagent session started here."
		if resumed {
			body = "eagent session resumed here."
		}
		if !resumed && strings.TrimSpace(prompt) != "" {
			body = prompt
		}
		req := finalechat.PostRequest{Body: body, Sender: "system", Format: "text", Notify: boolPtr(false), Title: p.title, Agent: p.agent, Meta: map[string]any{"eagent": "session", "kind": "session_start", "session_id": r.sess.ID}, ClientKey: p.key("start", strconv.Itoa(p.resumes))}
		if !resumed && strings.TrimSpace(prompt) != "" {
			req.Sender, req.Format = "user", "markdown"
		}
		msg, thread, err := p.client.Post(ctx, p.ref, req)
		if err != nil {
			r.ui.Log("finalechat: %v; the phone mirror is off for this session", err)
			r.post(func() { r.phone = nil })
			p.cancel()
			return
		}
		p.mu.Lock()
		p.posted[msg.ID] = true
		p.lastID = msg.ID
		p.threadID = thread.ID
		p.mu.Unlock()
		if resumed && p.mirror && strings.TrimSpace(prompt) != "" {
			// The message the session was resumed with, as the user wrote it.
			m2, _, err := p.client.Post(ctx, p.ref, finalechat.PostRequest{Body: prompt, Sender: "user", Notify: boolPtr(false), Meta: map[string]any{"eagent": "mirror", "seq": promptSeq}, ClientKey: p.key("u", strconv.FormatInt(promptSeq, 10))})
			if err == nil {
				p.mu.Lock()
				p.posted[m2.ID] = true
				p.lastID = m2.ID
				p.mu.Unlock()
			}
		}
		// Thread meta the app renders as chips: where the session runs and on what.
		host, _ := os.Hostname()
		meta := map[string]any{"cwd": r.opts.Project, "host": host, "model": models["orchestrator"]}
		if branch := gitBranch(ctx, r.opts.Project); branch != "" {
			meta["branch"] = branch
		}
		_, _ = p.client.Patch(ctx, p.ref, finalechat.PatchRequest{Meta: meta})
		remote := p.isRemote()
		r.post(func() {
			r.append(event.New(event.PhoneThread, event.ActorHarness, event.PhoneThreadData{ThreadID: thread.ID, ExternalID: strings.TrimPrefix(p.ref, "ext:"), BaseURL: p.client.BaseURL, RemoteMode: remote}))
		})
		r.ui.Log("finalechat: mirroring to your phone (thread %s, token from %s)", strings.TrimPrefix(p.ref, "ext:"), p.client.Source)
		if pending != nil {
			// A resumed session still waiting on a question asks it again,
			// since the earlier phone question expired or was never posted.
			q := *pending
			r.post(func() {
				if r.phone == p && r.st.Question != nil && r.st.Question.ID == q.ID {
					p.ask(r, q.ID, q.Text, q.Options)
				}
			})
		}
		p.wg.Add(2)
		go p.poll(r)
		go p.keeper(r)
	})
}

// enqueue schedules an outbound call; drops it if the mirror is closing.
func (p *phone) enqueue(fn func(context.Context)) {
	select {
	case p.queue <- fn:
	default:
		// A full queue means the service is unreachable; losing a mirror
		// message is better than blocking the loop.
	}
}

func (p *phone) worker() {
	defer p.wg.Done()
	for {
		select {
		case <-p.ctx.Done():
			return
		case fn := <-p.queue:
			fn(p.ctx)
		}
	}
}

// close clears the status line, posts the closing note, and waits for that
// post before cancelling the mirror: it is the last thing the phone sees,
// so the shutdown must not cut it off mid-flight.
func (p *phone) close(reason string) {
	sent := make(chan struct{})
	p.enqueue(func(context.Context) {
		defer close(sent)
		// Detached from p.ctx on purpose; bounded on its own.
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		if p.has("activity") {
			_ = p.client.ClearActivity(ctx, p.ref, time.Now().UnixNano())
		}
		body := "eagent session ended (" + reason + ")."
		switch reason {
		case "done":
			body = "eagent finished this session. Replies here are not read until it is resumed."
		case "awaiting-input":
			body = "eagent stopped here and is waiting for you. Resume the session to answer."
		case "interrupted":
			body = "eagent was interrupted. Resume the session to continue."
		}
		_, _, _ = p.client.Post(ctx, p.ref, finalechat.PostRequest{Body: body, Sender: "system", Format: "text", Notify: boolPtr(false), Meta: map[string]any{"eagent": "session-end", "kind": "session_end", "reason": reason}, ClientKey: p.key("end", reason, strconv.Itoa(p.resumes))})
	})
	// One FIFO worker: when the closing note has run, everything queued
	// before it has run too.
	select {
	case <-sent:
	case <-p.ctx.Done(): // the mirror already gave up (startup failure)
	case <-time.After(8 * time.Second):
	}
	p.cancel()
	p.wg.Wait()
}

func boolPtr(b bool) *bool { return &b }

// gitBranch names the checked-out branch of a project, or "" when it is not
// a git checkout or is on a detached head. Bounded so a slow disk cannot
// hold up the mirror.
func gitBranch(ctx context.Context, project string) string {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", project, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return ""
	}
	b := strings.TrimSpace(string(out))
	if b == "HEAD" {
		return ""
	}
	return b
}

// ---- outbound ---------------------------------------------------------------

// observe mirrors a newly recorded event. Loop goroutine only.
func (p *phone) observe(r *Runtime, ev event.Event) {
	p.noteState(r)
	seq := strconv.FormatInt(ev.Seq, 10)
	switch ev.Type {
	case event.NarratorMessage:
		var d event.NarratorMessageData
		_ = ev.Decode(&d)
		importance := "normal"
		if d.Important {
			importance = "important"
		}
		text, atts := d.Text, d.Attachments
		p.enqueue(func(ctx context.Context) {
			// The status the work is in right now rides on the message, so
			// it does not blink off while the orchestrator keeps going.
			act := p.attach()
			req := finalechat.PostRequest{Body: text, Importance: importance, Meta: map[string]any{"eagent": "narrator", "seq": ev.Seq}, Files: attachmentFiles(atts), ClientKey: p.key("m", seq), Activity: act}
			msg, thread, err := p.client.Post(ctx, p.ref, req)
			if err != nil && len(req.Files) > 0 {
				// The files may be refused (storage off, too large); the words still matter.
				req.Files = nil
				req.Body = text + "\n\n(" + attachmentSummary(atts) + " could not be uploaded)"
				req.ClientKey = p.key("m", seq, "text")
				msg, thread, err = p.client.Post(ctx, p.ref, req)
			}
			if err == nil {
				p.rememberIn(r, msg, thread)
				p.markSent(act)
			}
		})
	case event.NarratorQuestion:
		var d event.NarratorQuestionData
		_ = ev.Decode(&d)
		p.ask(r, d.ID, d.Text, d.Options)
	case event.UserAnswer:
		var d event.UserAnswerData
		_ = ev.Decode(&d)
		if d.Source == "finalechat" {
			return
		}
		// Answered elsewhere: withdraw the phone question and show the choice.
		qid, text := d.QuestionID, d.Text
		p.enqueue(func(ctx context.Context) {
			// Read the phone's id here, not at enqueue time: the ask call that
			// learns it runs on this same queue, just ahead of us.
			p.mu.Lock()
			fid := p.questions[qid]
			p.mu.Unlock()
			if fid != "" {
				_ = p.client.Cancel(ctx, fid)
			}
			if p.mirror {
				msg, _, err := p.client.Post(ctx, p.ref, finalechat.PostRequest{Body: text, Sender: "user", Notify: boolPtr(false), Meta: map[string]any{"eagent": "mirror", "kind": "answer", "question_id": d.QuestionID, "seq": ev.Seq}, ClientKey: p.key("a", seq)})
				if err == nil {
					p.remember(msg.ID)
				}
			}
		})
	case event.UserMessage:
		var d event.UserMessageData
		_ = ev.Decode(&d)
		if d.Source == "finalechat" || !p.mirror {
			return
		}
		text := d.Text
		p.enqueue(func(ctx context.Context) {
			msg, _, err := p.client.Post(ctx, p.ref, finalechat.PostRequest{Body: text, Sender: "user", Notify: boolPtr(false), Meta: map[string]any{"eagent": "mirror", "seq": ev.Seq}, ClientKey: p.key("u", seq)})
			if err == nil {
				p.remember(msg.ID)
			}
		})
	}
}

// ask poses a narrator question on the phone. Loop goroutine only.
func (p *phone) ask(r *Runtime, qid, text string, options []string) {
	var opts []finalechat.Option
	for _, o := range options {
		opts = append(opts, finalechat.Option{Label: clipLabel(o, 200)})
	}
	timeout := p.timeout
	p.mu.Lock()
	p.questions[qid] = "" // asked; the phone's id arrives when the call returns
	p.mu.Unlock()
	p.enqueue(func(ctx context.Context) {
		var act *finalechat.Activity
		if p.has("activity") {
			act = &finalechat.Activity{Text: "Waiting for your answer", Kind: "waiting", TTLSeconds: statusWaitTTL, Seq: time.Now().UnixNano()}
		}
		q, thread, err := p.client.Ask(ctx, p.ref, finalechat.AskRequest{Prompt: text, Options: opts, AllowFreeform: boolPtr(true), TimeoutSeconds: timeout, Meta: map[string]any{"eagent": "question", "question_id": qid}, ClientKey: p.key("q", qid), Activity: act})
		if err == nil {
			p.mu.Lock()
			if thread.ID != "" && p.threadID != "" && thread.ID != p.threadID {
				// The question recreated a deleted thread; the poll's anchor
				// belongs to the old one and the next page says so.
				p.threadID = thread.ID
			}
			p.mu.Unlock()
		}
		if err != nil {
			p.mu.Lock()
			p.gone[qid] = true
			p.mu.Unlock()
			r.post(func() {}) // let maybeEnd re-evaluate
			return
		}
		p.mu.Lock()
		p.questions[qid] = q.ID
		p.fromPhone[q.ID] = qid
		p.mu.Unlock()
		p.markSent(act)
		// A question is when remote mode matters most: re-read it now.
		p.refreshMe(r)
		p.wg.Add(1)
		go p.watchQuestion(r, q.ID, qid)
	})
}

func (p *phone) remember(id string) {
	p.mu.Lock()
	p.posted[id] = true
	p.mu.Unlock()
}

// rememberIn records a post and, when the thread that answered is not the
// one we knew (the user deleted it from the app and this post recreated it
// under the same id), moves the reply anchor to this post so the poll does
// not wait forever on an id the new thread never had.
func (p *phone) rememberIn(r *Runtime, msg finalechat.Message, thread finalechat.Thread) {
	p.mu.Lock()
	p.posted[msg.ID] = true
	if thread.ID != "" && p.threadID != "" && thread.ID != p.threadID {
		p.threadID = thread.ID
		p.lastID = msg.ID
		p.mu.Unlock()
		r.ui.Log("finalechat: the thread was recreated; replies are read from here on")
		return
	}
	p.mu.Unlock()
}

// clipLabel trims a label to n characters (runes, as the server counts them),
// never ending mid-character.
func clipLabel(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// pollFloorAfter is how quickly a long poll must return, with nothing, to
// count as "the server is not holding requests"; faster than this and the
// loop paces itself rather than asking again at once.
const pollFloorAfter = 30 * time.Second

// ---- inbound ------------------------------------------------------------------

// poll long-polls the thread for the user's replies and answers.
func (p *phone) poll(r *Runtime) {
	defer p.wg.Done()
	backoff := time.Second
	var floor time.Duration // pacing when the server answers at once with nothing
	saidRecreated := false
	for p.ctx.Err() == nil {
		p.mu.Lock()
		after := p.lastID
		p.mu.Unlock()
		start := time.Now()
		msgs, _, anchorUnknown, err := p.client.MessagesPage(p.ctx, p.ref, after, "user", 600, 100)
		if anchorUnknown && !saidRecreated {
			// The thread was deleted from the app and recreated under the same
			// id; the page restarts from its first message, and our own posts
			// are still skipped by id.
			r.ui.Log("finalechat: the thread was recreated; catching up from its first message")
		}
		saidRecreated = anchorUnknown
		if err == nil && len(msgs) == 0 && time.Since(start) < pollFloorAfter {
			// The long poll came straight back empty: the server did not hold
			// the request (an unknown anchor, or no wait support). Pace
			// ourselves instead of hammering the user's own account.
			if floor == 0 {
				floor = time.Second
			} else if floor < 30*time.Second {
				floor *= 2
			}
			select {
			case <-time.After(floor):
			case <-p.ctx.Done():
				return
			}
		} else if err == nil {
			floor = 0
		}
		if err != nil {
			if p.ctx.Err() != nil {
				return
			}
			select {
			case <-time.After(backoff):
			case <-p.ctx.Done():
				return
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
		for _, m := range msgs {
			p.mu.Lock()
			p.lastID = m.ID
			mine := p.posted[m.ID]
			p.mu.Unlock()
			if mine || m.Deleted || m.Sender != "user" || m.Origin == "token" {
				// Only the user speaking from the app counts; a token-posted
				// "user" message is an agent's mirror (ours or another's), and
				// a tombstone is a message the user took back.
				continue
			}
			body := strings.TrimSpace(m.Body)
			var atts []event.Attachment
			for _, a := range m.Attachments {
				saved, err := r.downloadAttachment(p.client, a)
				if err != nil {
					r.ui.Log("finalechat: could not fetch %s: %v", a.Filename, shortErr(err))
					continue
				}
				atts = append(atts, saved)
			}
			if body == "" && len(atts) == 0 {
				continue
			}
			if fid, ok := m.IsAnswer(); ok {
				p.mu.Lock()
				qid, known := p.fromPhone[fid]
				p.mu.Unlock()
				if !known {
					// A card from before this run (the thread outlives the
					// session id) or from an ask whose reply was lost. Recover
					// the eagent question id from the question's own meta; if
					// that fails, the answer must not bind to whatever question
					// happens to be open now.
					qid = "finalechat:" + fid
					if q, err := p.client.Question(p.ctx, fid, 0); err == nil {
						if id, _ := q.Meta["question_id"].(string); id != "" {
							qid = id
						}
					}
				}
				r.post(func() {
					r.handleInbox(InboxMessage{Type: "answer", Text: body, QuestionID: qid, From: "finalechat", Attachments: atts})
				})
				continue
			}
			r.post(func() {
				r.handleInbox(InboxMessage{Type: "message", Text: body, From: "finalechat", Attachments: atts})
			})
		}
	}
}

// watchQuestion notices when a phone question is cancelled or expires, so a
// batch session waiting on it can stop waiting. Answers arrive through the
// message poll.
func (p *phone) watchQuestion(r *Runtime, fid, qid string) {
	defer p.wg.Done()
	var floor time.Duration
	for p.ctx.Err() == nil {
		start := time.Now()
		q, err := p.client.Question(p.ctx, fid, 600)
		if err == nil && q.Status == "pending" && time.Since(start) < pollFloorAfter {
			// Answered at once and still pending: the server is not holding
			// the request. Pace the loop.
			if floor == 0 {
				floor = time.Second
			} else if floor < 30*time.Second {
				floor *= 2
			}
			select {
			case <-time.After(floor):
			case <-p.ctx.Done():
				return
			}
		} else if err == nil {
			floor = 0
		}
		if err != nil {
			if p.ctx.Err() != nil {
				return
			}
			select {
			case <-time.After(5 * time.Second):
			case <-p.ctx.Done():
				return
			}
			continue
		}
		switch q.Status {
		case "pending":
			continue
		case "answered":
			return
		case "dismissed":
			// The user declined from the phone: the question is over, and the
			// orchestrator hears that it must decide for itself.
			p.mu.Lock()
			p.gone[qid] = true
			p.mu.Unlock()
			r.post(func() {
				if r.st.Question == nil || r.st.Question.ID != qid {
					return
				}
				r.handleInbox(InboxMessage{Type: "answer", Text: dismissedAnswer, QuestionID: qid, From: "finalechat"})
			})
			return
		default: // cancelled | expired
			p.mu.Lock()
			p.gone[qid] = true
			p.mu.Unlock()
			r.post(func() {}) // let maybeEnd re-evaluate
			return
		}
	}
}

// waitingOnPhone reports whether a pending question can still be answered
// from the phone, which keeps a batch session alive. Loop goroutine only.
func (r *Runtime) waitingOnPhone() bool {
	p := r.phone
	if p == nil || r.st.Question == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.gone[r.st.Question.ID] {
		return false
	}
	_, asked := p.questions[r.st.Question.ID]
	return asked
}

func (p *phone) isRemote() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.remote
}

// PhoneStatus is the one-line description of the mirror given to the
// narrator and shown by `eagent view`.
func PhoneStatus(remote bool) string {
	if remote {
		return "The user reads you on their phone and has said they are away from the terminal (remote mode)"
	}
	return "The user also reads you on their phone"
}

var _ = fmt.Sprintf
