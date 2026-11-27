package harness

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/finalechat"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/store"
)

// TellPhoneStopped tells a session's phone thread what a run's own stop
// would, after a stop recorded on its record from outside any run (the
// server's Stop during its shutdown, or the next server finishing one), in
// the same order: the agent header as the record now has it (none
// running), the receipt of each of the user's messages as the record now
// settles it (one taken back unread turns to failed), the notes the sender
// is still owed (payOwed: each one posted is recorded with record, which
// appends to the session's record), and the closing note of a stop, keyed
// as a run's is (eagent:<id>:end:quit:<resumes>) so a stop told twice
// shows once. Whether to tell is the caller's, from the record; cfgs give
// only the accounts it may be on (whatever they say of enabled), and the
// first that has the session's thread is told (stoppedThread). It posts
// only to that thread, never making one, and tells nothing when no
// account has it. It returns why it told nothing, nil once told. The
// caller holds the session's lock.
func TellPhoneStopped(cfgs []config.Config, sessionPath string, record func(event.Event)) error {
	if finalechat.Disabled() {
		return errors.New("the phone is turned off (EAGENT_FINALECHAT)")
	}
	evs, err := store.Read(sessionPath)
	if err != nil {
		return err
	}
	cfg, client, me, thread, err := stoppedThread(cfgs, evs)
	if err != nil {
		return err
	}
	st := state.Replay(evs)
	sid := filepath.Base(sessionPath)
	ctx, cancel := context.WithCancel(context.Background())
	p := &phone{
		client: client, ref: thread, logf: func(string, ...any) {},
		sid: sid, resumes: st.Resumes, features: featureSet(me.Features),
		ctx: ctx, cancel: cancel, queue: make(chan func(context.Context), 4),
		posted: map[string]bool{}, owed: owedRefusals(evs),
	}
	p.wg.Add(1)
	go p.worker()
	// With the mirror's own functions, in a run's order: the header (as
	// after relay.end) and the receipts on its one worker, the owed notes
	// once those have gone, then the closing note.
	r := &Runtime{st: st, cfg: cfg, phone: p}
	r.publishRelayMeta()
	p.loadReceipts(r)
	p.mu.Lock()
	p.publishReceipts(r)
	sent := make(chan struct{})
	p.enqueue(func(context.Context) { close(sent) })
	select {
	case <-sent:
	case <-time.After(phoneCloseWait):
	}
	p.payOwed(record)
	p.close("quit")
	return nil
}

// stoppedThread is the thread of a session nobody runs, on the first of
// cfgs' accounts that has it, with that account's client and what its
// service offers (client keys, the status line, as the mirror learns them
// when it starts): the thread the record names (its latest phone.thread)
// on the first account that has it; when none does, the thread under the
// session's own reference, which is the one a run made again after the
// user deleted the first (its mirror took the new id; the record still
// names the old one). Reading a thread makes none. Only "not found" moves
// on to the next account or reference; any other failure is the answer.
func stoppedThread(cfgs []config.Config, evs []event.Event) (config.Config, *finalechat.Client, finalechat.Me, string, error) {
	named, ext := "", ""
	for _, ev := range evs {
		var d event.PhoneThreadData
		if ev.Type == event.PhoneThread && ev.Decode(&d) == nil && d.ThreadID != "" {
			named, ext = d.ThreadID, d.ExternalID
		}
	}
	if named == "" {
		return config.Config{}, nil, finalechat.Me{}, "", errors.New("its record names no phone thread")
	}
	refs := []string{named}
	if ext != "" {
		refs = append(refs, finalechat.Ref(ext))
	}
	why := errors.New("there is no phone account to tell it on (no token)")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for _, ref := range refs {
		for _, cfg := range cfgs {
			client, ok := finalechat.Resolve(cfg.Finalechat.TokenEnvName(), cfg.Finalechat.BaseURL)
			if !ok {
				continue
			}
			client.UserAgent = "eagent/" + Version
			me, err := client.Me(ctx)
			if err != nil {
				return cfg, nil, me, "", err
			}
			thread, err := client.Thread(ctx, ref)
			var gone *finalechat.Error
			if errors.As(err, &gone) && gone.Status == 404 {
				why = fmt.Errorf("its thread %s is not on the phone account at %s: %w", named, client.BaseURL, err)
				continue
			}
			if err != nil {
				return cfg, nil, me, "", err
			}
			if ref != named && thread.ID == "" {
				continue
			}
			if ref != named {
				named = thread.ID
			}
			return cfg, client, me, named, nil
		}
	}
	return config.Config{}, nil, finalechat.Me{}, "", why
}
