package integration

import (
	"context"
	"strings"

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
// writes a phone.read for the message it reads on from. So a run that
// wrote one kept the ledger; a run that did not (an eagent without it, as
// during an upgrade or a rollback, or one whose mirror never started) may
// have taken messages it did not record, and its record is read by time,
// as before.
type PhoneLedger struct {
	// Keeps: the last run in the record kept the ledger.
	Keeps bool
	// Cursor is the newest app message taken in, in the order the record
	// took them, which is the thread's order: a resumed session reads on
	// after it.
	Cursor string
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
	for _, ev := range evs {
		switch ev.Type {
		case event.SessionStart, event.SessionResume:
			l.Keeps = false // until this run's mirror says it keeps the ledger
		case event.PhoneRead:
			var d event.PhoneReadData
			if ev.Decode(&d) == nil && d.MessageID != "" {
				l.Keeps = true
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
// already holds (from the event stream), taken when the thread cannot be
// read or lists nothing newer. Both entry points (the sweep and the
// restore of hosted sessions) ask this.
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
		m, found, unknown, err := firstInThread(ctx, client, ext, l.Cursor, l.Takeable)
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
	m, found, _, err := firstInThread(ctx, client, ext, "", func(m finalechat.Message) bool {
		return l.Takeable(m) && m.CreatedAt.After(info.Modified)
	})
	if err != nil || !found {
		return fallback(err)
	}
	return m, true, nil
}

// firstInThread pages the user's messages after an anchor (from the start
// with none) and returns the first that ok accepts. unknown says the
// service does not know the anchor.
func firstInThread(ctx context.Context, client *finalechat.Client, ext, after string, ok func(finalechat.Message) bool) (finalechat.Message, bool, bool, error) {
	for i := 0; i < 50; i++ {
		msgs, _, unknown, err := client.MessagesPage(ctx, finalechat.Ref(ext), after, "user", 0, 100)
		if err != nil {
			return finalechat.Message{}, false, false, err
		}
		if unknown {
			return finalechat.Message{}, false, true, nil
		}
		for _, m := range msgs {
			after = m.ID
			if ok(m) {
				return m, true, false, nil
			}
		}
		if len(msgs) < 100 {
			break
		}
	}
	return finalechat.Message{}, false, false, nil
}
