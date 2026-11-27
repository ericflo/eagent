package harness

import (
	"context"
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
// shows once. cfg is the configuration the session ran on. It does nothing
// when that has no phone. The caller holds the session's lock.
func TellPhoneStopped(cfg config.Config, sessionPath string, record func(event.Event)) {
	fc := cfg.Finalechat
	if !fc.Wanted() || finalechat.Disabled() {
		return
	}
	client, ok := finalechat.Resolve(fc.TokenEnvName(), fc.BaseURL)
	if !ok {
		return
	}
	client.UserAgent = "eagent/" + Version
	evs, err := store.Read(sessionPath)
	if err != nil {
		return
	}
	// What the service offers (client keys, the status line), as the
	// mirror learns it when it starts.
	meCtx, meCancel := context.WithTimeout(context.Background(), 15*time.Second)
	me, err := client.Me(meCtx)
	meCancel()
	if err != nil {
		return
	}
	st := state.Replay(evs)
	sid := filepath.Base(sessionPath)
	ctx, cancel := context.WithCancel(context.Background())
	p := &phone{
		client: client, ref: finalechat.Ref("eagent:" + sid), logf: func(string, ...any) {},
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
}
