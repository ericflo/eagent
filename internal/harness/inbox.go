package harness

import (
	"encoding/json"
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
// processed in name order and deleted once recorded.

// InboxDir is where messages for a session are dropped.
func InboxDir(sessionPath string) string { return filepath.Join(sessionPath, "inbox") }

// InboxMessage is one dropped file.
type InboxMessage struct {
	Type       string `json:"type"` // message | answer | stop
	Text       string `json:"text,omitempty"`
	QuestionID string `json:"question_id,omitempty"`
	From       string `json:"from,omitempty"` // free-form origin, e.g. "web"
	// Client declares what context the sending client can supply.
	Client *clientcaps.Caps `json:"client,omitempty"`
	// Attachments are files already saved under the session directory.
	Attachments []event.Attachment `json:"attachments,omitempty"`
}

// PostInbox writes a message for the session at sessionPath.
func PostInbox(sessionPath string, msg InboxMessage) error {
	dir := InboxDir(sessionPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	name := filepath.Join(dir, time.Now().UTC().Format("20060102T150405.000000000")+".json")
	tmp := name + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, name) // atomic: readers never see a partial file
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
	for _, name := range names {
		path := filepath.Join(dir, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		_ = os.Remove(path)
		var msg InboxMessage
		if json.Unmarshal(raw, &msg) != nil {
			continue
		}
		if msg.Type == "stop" && r.sess.Stale[name] {
			// A stop that was already in the inbox when this run took the
			// session lock was meant for an earlier run; it must not quit
			// this one before its first tick. Messages and answers are kept.
			r.ui.Log("inbox: ignoring a stop left by an earlier run")
			continue
		}
		r.handleInbox(msg)
	}
}

func (r *Runtime) handleInbox(msg InboxMessage) {
	text := strings.TrimSpace(msg.Text)
	switch msg.Type {
	case "stop":
		r.beginShutdown("quit", 0)
	case "answer":
		if q := r.st.Question; q != nil && (msg.QuestionID == "" || msg.QuestionID == q.ID) && text != "" {
			r.ui.Idle(false)
			r.append(event.New(event.UserAnswer, event.ActorUser, event.UserAnswerData{QuestionID: q.ID, Text: text, Source: msg.From, Client: msg.Client, Attachments: msg.Attachments}))
			return
		}
		if text != "" || len(msg.Attachments) > 0 { // no matching question: treat as a message
			r.ui.Idle(false)
			r.append(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: text, Source: msg.From, Client: msg.Client, Attachments: msg.Attachments}))
		}
	case "message", "":
		if text == "" && len(msg.Attachments) == 0 {
			return
		}
		if strings.HasPrefix(text, "/") {
			r.slashCommand(text)
			return
		}
		r.onInputFrom(text, msg.From, msg.Client, msg.Attachments...)
	}
}
