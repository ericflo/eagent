package integration

import (
	"context"
	"strings"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/finalechat"
	"github.com/ericflo/eagent/internal/store"
)

// PhoneLedger is what a session's record says of the messages the user
// sent from the app that the session took in. Every message the session
// takes in leaves its id in the record in the step that takes it: on the
// user.message or user.answer it became (phone_id, and card_id for a
// card's answer), or as a phone.read when it became something else (a
// command, nothing, nothing but refused files). Every mirror that starts
// writes a phone.read for the message it reads on from: the cursor.
//
// Whether a run keeps the ledger is said by its first write: the
// session.start or session.resume of a run by this eagent has
// phone_ledger set. A run whose mirror never started (the phone off, the
// service down, a hard stop in its first seconds) still keeps it: it takes
// in nothing from the app but the words it was resumed with, which carry
// their id. A run by an eagent without the ledger (an upgrade, a rollback)
// may have taken messages it did not record: its record is read by time,
// and a ledger run after it starts afresh, judging what came before it by
// the time the run before it stopped (the resume says where that was).
type PhoneLedger struct {
	// Keeps: the last run in the record kept the ledger.
	Keeps bool
	// Cursor is the newest app message taken in since the ledger last
	// started, in the order the record took them, which is the thread's
	// order: a resumed session reads on after it. Empty when the runs that
	// kept the ledger have taken nothing yet.
	Cursor string
	// Since is when the ledger last started: messages written before it
	// were for runs that did not keep it, and are judged by time. Zero for
	// a session that kept the ledger from its start.
	Since time.Time
	// Taken holds every app message id the record names; Cards the cards
	// whose answer was taken (phone.answer_taken), so a card's answer
	// message is not taken a second time.
	Taken map[string]bool
	Cards map[string]bool
}

// ReadPhoneLedger reads the ledger off a session's events. A process that
// has begun a run passes the events before that run's start.
func ReadPhoneLedger(evs []event.Event) PhoneLedger {
	l := PhoneLedger{Taken: map[string]bool{}, Cards: map[string]bool{}}
	took := func(id string) {
		if id != "" {
			l.Cursor, l.Taken[id] = id, true
		}
	}
	card := func(id string) {
		if id != "" {
			l.Cards[id] = true
		}
	}
	var last time.Time // the time of the event before this one
	for i, ev := range evs {
		switch ev.Type {
		case event.SessionStart, event.SessionResume:
			var d struct {
				PhoneLedger bool  `json:"phone_ledger"`
				After       int64 `json:"after"`
			}
			_ = ev.Decode(&d)
			if d.PhoneLedger && !l.Keeps {
				// The ledger starts here: what came before was taken (or
				// not) by runs that did not record it. "Before" ends where
				// that run stopped, which the resume names (after): the
				// events between there and here are the resume's own
				// repairs of a cut turn, written at the resume's time.
				l.Cursor, l.Since = "", last
				for j := i - 1; d.After > 0 && j >= 0; j-- {
					if evs[j].Seq <= d.After {
						l.Since = evs[j].Time
						break
					}
				}
			}
			l.Keeps = d.PhoneLedger
		case event.PhoneRead:
			var d event.PhoneReadData
			if ev.Decode(&d) == nil && d.MessageID != "" {
				took(d.MessageID)
			}
		case event.UserMessage:
			var d event.UserMessageData
			if ev.Decode(&d) == nil {
				took(d.PhoneID)
				card(d.CardID)
			}
		case event.UserAnswer:
			var d event.UserAnswerData
			if ev.Decode(&d) == nil {
				took(d.PhoneID)
				card(d.CardID)
			}
		case event.PhoneAnswerTaken:
			var d event.PhoneAnswerTakenData
			if ev.Decode(&d) == nil && d.CardID != "" {
				l.Cards[d.CardID] = true
			}
		}
		last = ev.Time
	}
	return l
}

// Takeable reports whether m is a message from the user, written in the
// app, with something in it, that the ledger does not hold: one a resumed
// session must take in.
func (l PhoneLedger) Takeable(m finalechat.Message) bool {
	if m.ID == "" || m.Sender != "user" || m.Origin == "token" || m.Deleted {
		return false
	}
	if strings.TrimSpace(m.Body) == "" && len(m.Attachments) == 0 {
		return false
	}
	if l.Taken[m.ID] {
		return false
	}
	if card, ok := m.IsAnswer(); ok && l.Cards[card] {
		return false // the card's answer reached the session another way
	}
	return true
}

// nextPhoneMessage is the earliest app message in the session's thread
// that the session has not taken in: the one to resume it with. The
// session's poll then reads on after it, in order, so the rest are taken
// in by the running session. With the ledger, that is the first takeable
// message after the cursor; without it (or when the thread no longer has
// the cursor's message), the first one written after the session's last
// log write, as the sweep always judged. trigger is a message the caller
// already holds (the event stream just brought it, or it is a kept reply
// the thread still has: takenBack), taken when the thread cannot be read
// or lists nothing newer. Both entry points (the sweep and the restore of
// hosted sessions) ask this.
func nextPhoneMessage(ctx context.Context, client *finalechat.Client, info store.Info, trigger *finalechat.Message) (finalechat.Message, bool, error) {
	evs, err := store.Read(info.Path)
	if err != nil {
		return finalechat.Message{}, false, err
	}
	l := ReadPhoneLedger(evs)
	ext := "eagent:" + info.ID
	fallback := func(err error) (finalechat.Message, bool, error) {
		if err != nil && l.Keeps {
			// An earlier message may wait in the thread we could not read;
			// the trigger would jump it, and the cursor would then pass it
			// for good. The next sweep, which reads the thread, resumes the
			// session in order.
			return finalechat.Message{}, false, err
		}
		if trigger != nil && l.Takeable(*trigger) {
			// The thread lists nothing newer (its listing can lag the
			// stream), or a record without the ledger, as before.
			return *trigger, true, nil
		}
		return finalechat.Message{}, false, err
	}
	if l.Keeps && l.Cursor != "" {
		m, _, found, unknown, err := firstInThread(ctx, client, ext, l.Cursor, time.Time{}, l.Takeable)
		if err != nil {
			return fallback(err)
		}
		if !unknown {
			if found {
				return m, true, nil
			}
			return fallback(nil)
		}
		// The thread no longer has the cursor's message (it was taken back,
		// or the thread was recreated): judge by time.
	}
	m, _, found, err := l.Waiting(ctx, client, ext, info.Modified)
	if err != nil || !found {
		return fallback(err)
	}
	return m, true, nil
}

// takenBack says whether the thread, read now, no longer has m, a message
// the caller holds a copy of: the user deleted it since the copy was made.
// (The service lists a user's messages without the deleted ones, after a
// cursor too, so reading on from the session's cursor does not say.) A
// copy that does not say when it was written cannot be looked up, and is
// not called taken back.
func takenBack(ctx context.Context, client *finalechat.Client, ext string, m finalechat.Message) (bool, error) {
	if m.CreatedAt.IsZero() {
		return false, nil
	}
	got, _, found, _, err := firstInThread(ctx, client, ext, "", m.CreatedAt.Add(-time.Millisecond), func(got finalechat.Message) bool { return got.ID == m.ID })
	if err != nil {
		return false, err
	}
	return !found || got.Deleted, nil
}

// Waiting is the earliest app message in the thread that waited for the
// session, judged by time: written after the session's last write
// (lastWrite) for a record without the ledger; for one that keeps it,
// after the ledger started, since every message its runs took is in it (a
// ledger run that took nothing yet, as one cut off before its mirror's
// first post). prev is the user message listed just before it, empty when
// it is the first: reading on after prev reads it first. The reply
// watcher resumes with it; a session resumed with no app message reads on
// after prev.
func (l PhoneLedger) Waiting(ctx context.Context, client *finalechat.Client, ext string, lastWrite time.Time) (m finalechat.Message, prev string, found bool, err error) {
	since := lastWrite
	if l.Keeps {
		since = l.Since
	}
	m, prev, found, _, err = firstInThread(ctx, client, ext, "", since, func(m finalechat.Message) bool {
		return l.Takeable(m) && m.CreatedAt.After(since)
	})
	return m, prev, found, err
}

// firstInThread pages the user's messages after an anchor (with none,
// those written after since) and returns the first that ok accepts, with
// the one listed before it in the thread (the anchor for the first listed;
// "" when the thread has none before it). unknown says the service does
// not know the anchor.
func firstInThread(ctx context.Context, client *finalechat.Client, ext, after string, since time.Time, ok func(finalechat.Message) bool) (finalechat.Message, string, bool, bool, error) {
	ref := finalechat.Ref(ext)
	for i := 0; i < 50; i++ {
		var msgs []finalechat.Message
		var unknown bool
		var err error
		if after == "" {
			// Forward from since. Asked with no cursor the service lists the
			// thread's newest page, and the earliest message that waited
			// can be older than that page.
			msgs, err = client.MessagesSince(ctx, ref, since, "user", 100)
		} else {
			msgs, _, unknown, err = client.MessagesPage(ctx, ref, after, "user", 0, 100)
		}
		if err != nil {
			return finalechat.Message{}, "", false, false, err
		}
		if unknown {
			return finalechat.Message{}, "", false, true, nil
		}
		for _, m := range msgs {
			prev := after
			after = m.ID
			if ok(m) {
				if prev == "" {
					// The first written after since: the one before it is
					// older than what was listed.
					if prev, err = client.MessageBefore(ctx, ref, m, "user"); err != nil {
						return finalechat.Message{}, "", false, false, err
					}
				}
				return m, prev, true, false, nil
			}
		}
		if len(msgs) < 100 {
			break
		}
	}
	return finalechat.Message{}, "", false, false, nil
}
