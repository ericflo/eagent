package harness

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
	prompt := ""
	if !resumed {
		for _, ev := range r.st.Events {
			if ev.Type == event.UserMessage {
				var d event.UserMessageData
				_ = ev.Decode(&d)
				prompt = d.Text
				break
			}
		}
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
		p.mu.Unlock()
		body := "eagent session started here."
		if resumed {
			body = "eagent session resumed here."
		}
		if strings.TrimSpace(prompt) != "" {
			body = prompt
		}
		req := finalechat.PostRequest{Body: body, Sender: "system", Format: "text", Notify: boolPtr(false), Title: p.title, Agent: p.agent, Meta: map[string]any{"eagent": "session", "session_id": r.sess.ID}}
		if strings.TrimSpace(prompt) != "" {
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
		p.wg.Add(1)
		go p.poll(r)
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

// close posts the closing note and waits briefly for the queue to drain.
func (p *phone) close(reason string) {
	p.enqueue(func(ctx context.Context) {
		body := "eagent session ended (" + reason + ")."
		switch reason {
		case "done":
			body = "eagent finished this session. Replies here are not read until it is resumed."
		case "awaiting-input":
			body = "eagent stopped here and is waiting for you. Resume the session to answer."
		case "interrupted":
			body = "eagent was interrupted. Resume the session to continue."
		}
		_, _, _ = p.client.Post(ctx, p.ref, finalechat.PostRequest{Body: body, Sender: "system", Format: "text", Notify: boolPtr(false), Meta: map[string]any{"eagent": "session-end", "reason": reason}})
	})
	done := make(chan struct{})
	go func() {
		// Let the worker get through what is queued, then stop.
		deadline := time.After(8 * time.Second)
		for {
			select {
			case <-deadline:
				close(done)
				return
			default:
			}
			if len(p.queue) == 0 {
				time.Sleep(150 * time.Millisecond) // the in-flight call
				close(done)
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()
	<-done
	p.cancel()
	p.wg.Wait()
}

func boolPtr(b bool) *bool { return &b }

// ---- outbound ---------------------------------------------------------------

// observe mirrors a newly recorded event. Loop goroutine only.
func (p *phone) observe(r *Runtime, ev event.Event) {
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
			req := finalechat.PostRequest{Body: text, Importance: importance, Meta: map[string]any{"eagent": "narrator", "seq": ev.Seq}, Files: attachmentFiles(atts)}
			msg, _, err := p.client.Post(ctx, p.ref, req)
			if err != nil && len(req.Files) > 0 {
				// The files may be refused (storage off, too large); the words still matter.
				req.Files = nil
				req.Body = text + "\n\n(" + attachmentSummary(atts) + " could not be uploaded)"
				msg, _, err = p.client.Post(ctx, p.ref, req)
			}
			if err == nil {
				p.remember(msg.ID)
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
		p.mu.Lock()
		fid := p.questions[d.QuestionID]
		p.mu.Unlock()
		text := d.Text
		p.enqueue(func(ctx context.Context) {
			if fid != "" {
				_ = p.client.Cancel(ctx, fid)
			}
			if p.mirror {
				msg, _, err := p.client.Post(ctx, p.ref, finalechat.PostRequest{Body: text, Sender: "user", Notify: boolPtr(false), Meta: map[string]any{"eagent": "mirror", "kind": "answer", "question_id": d.QuestionID, "seq": ev.Seq}})
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
			msg, _, err := p.client.Post(ctx, p.ref, finalechat.PostRequest{Body: text, Sender: "user", Notify: boolPtr(false), Meta: map[string]any{"eagent": "mirror", "seq": ev.Seq}})
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
		q, _, err := p.client.Ask(ctx, p.ref, finalechat.AskRequest{Prompt: text, Options: opts, AllowFreeform: boolPtr(true), TimeoutSeconds: timeout, Meta: map[string]any{"eagent": "question", "question_id": qid}})
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
		p.wg.Add(1)
		go p.watchQuestion(r, q.ID, qid)
	})
}

func (p *phone) remember(id string) {
	p.mu.Lock()
	p.posted[id] = true
	p.mu.Unlock()
}

func clipLabel(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n-1] + "…"
	}
	return s
}

// ---- inbound ------------------------------------------------------------------

// poll long-polls the thread for the user's replies and answers.
func (p *phone) poll(r *Runtime) {
	defer p.wg.Done()
	backoff := time.Second
	for p.ctx.Err() == nil {
		p.mu.Lock()
		after := p.lastID
		p.mu.Unlock()
		msgs, _, err := p.client.Messages(p.ctx, p.ref, after, "user", 600, 100)
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
			if mine || m.Sender != "user" {
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
				qid := p.fromPhone[fid]
				p.mu.Unlock()
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
	for p.ctx.Err() == nil {
		q, err := p.client.Question(p.ctx, fid, 600)
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
