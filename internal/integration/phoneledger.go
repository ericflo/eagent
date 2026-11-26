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
// user.message it became (phone_id), or as a phone.read when it became
// something else (an answer, a command, nothing but refused files). A
// mirror that starts keeping the ledger writes a phone.read for the
// message it reads on from, so a record that holds any phone.read keeps
// the ledger from that point on; one that holds none (written before the
// ledger, or by an eagent without it) is read by time, as it was.
type PhoneLedger struct {
	// Keeps: the record keeps the ledger.
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

// ReadPhoneLedger reads the ledger off a session's events.
func ReadPhoneLedger(evs []event.Event) PhoneLedger {
	l := PhoneLedger{Taken: map[string]bool{}, Cards: map[string]bool{}}
	for _, ev := range evs {
		switch ev.Type {
		case event.PhoneRead:
			var d event.PhoneReadData
			if ev.Decode(&d) == nil && d.MessageID != "" {
				l.Keeps, l.Cursor, l.Taken[d.MessageID] = true, d.MessageID, true
			}
		case event.UserMessage:
			var d event.UserMessageData
			if ev.Decode(&d) == nil && d.PhoneID != "" {
				l.Cursor, l.Taken[d.PhoneID] = d.PhoneID, true
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
		if trigger != nil && l.Takeable(*trigger) {
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

// sweepSlack is how long before a session's last log write a message may
// have been written and still not be taken in: a session stopped hard
// right after it received one writes little or nothing after it. Only a
// record that keeps the ledger can tell such a message from one it took.
const sweepSlack = 10 * time.Minute
