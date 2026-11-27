package harness

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ericflo/eagent/internal/clientcaps"
	"github.com/ericflo/eagent/internal/event"
)

// The inbox lets other processes (the web UI, a script) talk to a running
// session without owning it: they drop small JSON files into
// <session>/inbox/ and the runtime folds them into the log. Files are
// processed in name order. One that gives its own name as its id, as
// PostInbox's do, is deleted once it is recorded and is never taken twice;
// any other is first given a name and an id of its own (claimInbox).

// InboxDir is where messages for a session are dropped.
func InboxDir(sessionPath string) string { return filepath.Join(sessionPath, "inbox") }

// InboxMessage is one dropped file.
type InboxMessage struct {
	Type       string `json:"type"` // message | answer | dismiss | stop
	Text       string `json:"text,omitempty"`
	QuestionID string `json:"question_id,omitempty"`
	From       string `json:"from,omitempty"` // free-form origin, e.g. "web"
	// Client declares what context the sending client can supply.
	Client *clientcaps.Caps `json:"client,omitempty"`
	// Attachments are files already saved under the session directory.
	Attachments []event.Attachment `json:"attachments,omitempty"`
	// Route and Delivery carry an explicit "to eagent / to the agent" and
	// "interrupt / after this turn" choice from the client (relay sessions).
	Route    string `json:"route,omitempty"`
	Delivery string `json:"delivery,omitempty"`
	// PhoneID is the Finalechat message id of a message from the phone.
	PhoneID string `json:"phone_id,omitempty"`
	// CardID is the phone card an answer was written on.
	CardID string `json:"card_id,omitempty"`
}

// HeldWhileEnding is the held reason a user.message is recorded with when
// a run that is ending takes it: kept for the next run, not answered
// (routeUserMessage).
const HeldWhileEnding = "the session was ending"

// PostInbox writes a message for the session at sessionPath.
func PostInbox(sessionPath string, msg InboxMessage) error {
	_, err := PostInboxFile(sessionPath, msg)
	return err
}

// PostInboxFile is PostInbox, and says which file it wrote: the run that
// takes the message removes that file once it is recorded.
func PostInboxFile(sessionPath string, msg InboxMessage) (string, error) {
	dir := InboxDir(sessionPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	// The file says its own name ("inbox_id"), which marks that name as
	// this submission's and no other's (see inboxFileID). The time orders
	// the files; the random part keeps two posts in the same instant
	// apart, and so does a temporary file of each post's own.
	stamp, err := inboxStamp()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, stamp+".json"), writeInbox(dir, stamp, msg)
}

// inboxStamp is a new name for an inbox file: the time, and a random part.
func inboxStamp() (string, error) {
	var salt [4]byte
	if _, err := rand.Read(salt[:]); err != nil {
		return "", err
	}
	return time.Now().UTC().Format("20060102T150405.000000000") + "-" + hex.EncodeToString(salt[:]), nil
}

// writeInbox writes msg to dir as the file stamp.json, which gives stamp as
// its id, in place of any file of that name.
func writeInbox(dir, stamp string, msg InboxMessage) error {
	raw, err := json.Marshal(struct {
		InboxMessage
		ID string `json:"inbox_id"`
	}{msg, stamp})
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "post-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(raw)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp, 0o644)
	}
	if err == nil {
		err = os.Rename(tmp, filepath.Join(dir, stamp+".json")) // atomic: readers never see a partial file
	}
	if err != nil {
		_ = os.Remove(tmp)
	}
	return err
}

// Take writes msg into the session's inbox and reads the inbox at once, on
// the run's loop, unless the run has begun to end: then nothing is
// written, and Take reports false. Handed over this way, words are taken
// before Take returns or not at all; a file left from outside can come
// after an ending run's last look. A file the poll could not take is taken
// back, and that is the error; one that cannot be taken back is left for
// a later look, and reported taken.
func (r *Runtime) Take(msg InboxMessage) (took bool, err error) {
	r.sync(func() {
		if r.ending {
			return
		}
		dir := InboxDir(r.sess.Path)
		stamp, serr := inboxStamp()
		if err = serr; err == nil {
			err = os.MkdirAll(dir, 0o755)
		}
		if err == nil {
			err = writeInbox(dir, stamp, msg)
		}
		if err != nil {
			return
		}
		r.pollInbox()
		if took = r.inboxTaken[stamp]; !took {
			if os.Remove(filepath.Join(dir, stamp+".json")) == nil {
				err = errors.New("the session could not read it from its inbox")
			} else {
				took = true // left for a later look
			}
		}
	})
	return took, err
}

// pollInbox drains pending inbox files. Loop goroutine only.
func (r *Runtime) pollInbox() {
	dir := InboxDir(r.sess.Path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	leaving := false // a file was left for the next run in this look
	for _, name := range names {
		if r.recordFailed {
			// Nothing is taken off the inbox that cannot go on the
			// record: the next run takes it.
			return
		}
		path := filepath.Join(dir, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		id := inboxFileID(raw, name)
		var msg InboxMessage
		bad := json.Unmarshal(raw, &msg) != nil
		if id == "" && !bad {
			path, id = claimInbox(dir, path, msg)
		}
		// A file with no id (one written by hand that could not be given
		// one) is handled as every inbox file was before files had ids:
		// removed, then taken, and nothing is remembered of it. With an
		// id, a stop or a dismissal still has its file removed
		// first (it leaves no event to know it by, so a cut in between
		// loses it and never repeats it); a command is recorded as taken
		// (inbox.taken) before that, as a command from the phone is, so a
		// cut after the record loses it and never repeats it; and words
		// are recorded, with the id, before their file is removed: a file
		// still here whose id this run handled, or that an earlier run
		// left and the record holds, is not taken again.
		switch {
		case bad:
			_ = os.Remove(path)
		case id != "" && (r.inboxTaken[id] || r.sess.Stale[name] && r.inboxRecorded(id)):
			_ = os.Remove(path)
		case msg.Type == "stop" && r.sess.Stale[name]:
			// A stop that was already in the inbox when this run took the
			// session lock was meant for an earlier run; it must not quit
			// this one before its first tick. Messages and answers are kept.
			_ = os.Remove(path)
			r.ui.Log("inbox: ignoring a stop left by an earlier run")
		case leaving || (r.ending || r.loopGone()) && (id == "" || !inboxWords(msg, r.ownCommand)):
			// A run that is ending carries out nothing more (its work
			// would fail, or its answer go unread once the loop has
			// closed): a command, a stop or a dismissal stays in the inbox
			// for the next run, as on main, and so does every file after
			// it, which the next run then takes in the order written.
			// Words before it are recorded, kept for the next run (below).
			leaving = true
			continue
		case id == "" || !inboxWords(msg, r.ownCommand):
			if id != "" && (msg.Type == "message" || msg.Type == "") {
				r.append(event.New(inboxTakenEvent, event.ActorHarness, map[string]string{"inbox": id, "text": strings.TrimSpace(msg.Text)}))
				if r.recordFailed {
					return // not taken: its file stays
				}
			}
			_ = os.Remove(path)
			r.handleInboxFile(msg, id)
		default:
			r.handleInboxFile(msg, id)
			if r.recordFailed {
				return // not on the record: its file stays
			}
			_ = os.Remove(path)
		}
		if id != "" {
			if r.inboxTaken == nil {
				r.inboxTaken = map[string]bool{}
			}
			r.inboxTaken[id] = true
		}
	}
}

// claimInbox gives an inbox file with no id of its own (one written by
// hand, under a name its writer may use again) a new name, and writes it
// again under that name with the name as its id, so that it is taken as
// PostInbox's files are: recorded before it is removed, and once. It is
// moved first, so a cut leaves one file: one with no id under the new
// name is given an id by the next look. It returns the file's path and id:
// the old path and none when it could not be moved, the new path and none
// when it could not be written again.
func claimInbox(dir, path string, msg InboxMessage) (string, string) {
	stamp, err := inboxStamp()
	if err != nil {
		return path, ""
	}
	to := filepath.Join(dir, stamp+".json")
	if os.Rename(path, to) != nil {
		return path, ""
	}
	if writeInbox(dir, stamp, msg) != nil {
		return to, ""
	}
	return to, stamp
}

// inboxFileID is the id of the submission in an inbox file: the file's
// name without ".json", when the file itself gives that as its "inbox_id"
// (PostInbox's do). A name alone cannot serve, because a writer may use one
// name again for its next message. Any other file has no id.
func inboxFileID(raw []byte, name string) string {
	var d struct {
		ID string `json:"inbox_id"`
	}
	_ = json.Unmarshal(raw, &d)
	if d.ID == "" || d.ID+".json" != name {
		return ""
	}
	return d.ID
}

// inboxWords reports whether an inbox message is words of the user's (it
// becomes a user.message or user.answer, or nothing), as opposed to a
// command of eagent's own, a stop or a dismissal.
func inboxWords(msg InboxMessage, command func(line, source string) bool) bool {
	switch msg.Type {
	case "answer":
		return true
	case "message", "":
		return !command(strings.TrimSpace(msg.Text), msg.From)
	}
	return false
}

// inboxTakenEvent records an eagent command from an inbox file as taken,
// before it is carried out.
const inboxTakenEvent = "inbox.taken"

// inboxRecorded reports whether the record holds the user.message or
// user.answer that the inbox submission id became, or its inbox.taken.
// Loop goroutine only.
func (r *Runtime) inboxRecorded(id string) bool {
	for _, ev := range r.st.Events {
		var d struct {
			Inbox string `json:"inbox"`
		}
		if (ev.Type == event.UserMessage || ev.Type == event.UserAnswer || ev.Type == inboxTakenEvent) && ev.Decode(&d) == nil && d.Inbox == id {
			return true
		}
	}
	return false
}

func (r *Runtime) handleInbox(msg InboxMessage) { r.handleInboxFile(msg, "") }

// handleInboxFile takes msg; file is the id of the inbox submission it
// came in ("" for none), carried by the user.message or user.answer it
// becomes.
func (r *Runtime) handleInboxFile(msg InboxMessage, file string) {
	text := strings.TrimSpace(msg.Text)
	switch msg.Type {
	case "stop":
		r.stopSession("the user stopped the session")
	case "answer":
		if q := r.st.Question; q != nil && (msg.QuestionID == "" || msg.QuestionID == q.ID) && text != "" {
			r.ui.Idle(false)
			r.append(event.New(event.UserAnswer, event.ActorUser, event.UserAnswerData{QuestionID: q.ID, Text: text, Source: msg.From, Client: msg.Client, Attachments: msg.Attachments, PhoneID: msg.PhoneID, CardID: msg.CardID, Inbox: file}))
			return
		}
		if text != "" || len(msg.Attachments) > 0 { // no matching question: treat as a message
			r.ui.Idle(false)
			r.userMessage(event.UserMessageData{Text: text, Source: msg.From, Client: msg.Client, Attachments: msg.Attachments, PhoneID: msg.PhoneID, CardID: msg.CardID, Inbox: file})
		}
	case "dismiss":
		r.dismissQuestion(msg.QuestionID, msg.From)
	case "message", "":
		if text == "" && len(msg.Attachments) == 0 {
			return
		}
		if r.ownCommand(text, msg.From) {
			r.slashCommandFrom(text, msg.From, msg.Client)
			return
		}
		if msg.Route != "" || msg.Delivery != "" || msg.PhoneID != "" {
			if q := r.st.Question; q == nil {
				r.ui.Idle(false)
				r.userMessage(event.UserMessageData{Text: text, Source: msg.From, Client: msg.Client, Attachments: msg.Attachments, Route: msg.Route, Delivery: msg.Delivery, PhoneID: msg.PhoneID, Inbox: file})
				return
			}
		}
		r.onInputFrom(text, msg.From, msg.PhoneID, file, msg.Client, msg.Attachments...)
	}
}

// ownCommand reports whether a line the user wrote is a command for eagent
// itself and not words for the agent or for eagent's model. With an agent
// running, a slash word eagent does not own is the agent's. With none, it
// is a command (an unknown one gets "try /help") when typed in eagent's
// terminal and no question is open. Otherwise anything that is not one of
// eagent's commands is words for eagent's model (or the answer), which may
// well begin with a slash (a path): "try /help" would show only in a
// terminal, and the phone or the web would see the message vanish. For the
// same reason a command whose output shows only in eagent's terminal, and
// /dismiss with no question open, are words when they come from the phone
// or the web, with an agent running (relayPassthrough) or not. Loop
// goroutine.
func (r *Runtime) ownCommand(line, source string) bool {
	if !strings.HasPrefix(line, "/") || r.relayPassthrough(line, source) {
		return false
	}
	if source != "" {
		switch strings.ToLower(strings.Fields(line)[0]) {
		case "/status", "/s", "/schedules", "/help", "/?", "/tasks", "/procs":
			return false
		case "/dismiss":
			if r.st.Question == nil {
				return false
			}
		}
	}
	if (r.st.Question != nil || source != "") && len(r.st.ActiveRelays()) == 0 && !eagentCommand(line) {
		return false
	}
	return true
}

// eagentCommand reports whether a line that starts with "/" is one of
// eagent's own commands, today's or one it used to have and still answers
// for. Anything else that starts with a slash is the agent's command, or
// just words.
func eagentCommand(line string) bool {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return false
	}
	switch cmd := strings.ToLower(fields[0]); cmd {
	case "/quit", "/exit", "/q", "/dismiss", "/status", "/s", "/schedules", "/help", "/?", "/tasks", "/procs", "/kill":
		return true
	default:
		return relayCommands[cmd]
	}
}
