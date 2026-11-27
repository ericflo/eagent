package harness

import (
	"context"
	"path/filepath"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/finalechat"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/store"
)

// TellPhoneStopped tells a session's phone thread what a run's own stop
// would, after a stop recorded on its record from outside any run (the
// server's Stop during its shutdown, or the next server finishing one): the
// receipt of each of the user's messages as the record now settles it (one
// taken back unread turns to failed), and the closing note of a stop. It
// does nothing when the project has no phone, and writes nothing to the
// record. The session must not be running.
func TellPhoneStopped(project, sessionPath string) {
	cfg, err := config.LoadBundle(project, "", "")
	if err != nil {
		return
	}
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
	st := state.Replay(evs)
	sid := filepath.Base(sessionPath)
	ctx, cancel := context.WithCancel(context.Background())
	p := &phone{
		client: client, ref: finalechat.Ref("eagent:" + sid), logf: func(string, ...any) {},
		sid: sid, resumes: st.Resumes, features: map[string]bool{},
		ctx: ctx, cancel: cancel, queue: make(chan func(context.Context), 4),
	}
	p.wg.Add(1)
	go p.worker()
	// The receipts as the run's mirror computes them from the record, and
	// sends them; then the closing note, after them on the one worker.
	r := &Runtime{st: st}
	p.loadReceipts(r)
	p.mu.Lock()
	p.publishReceipts(r)
	p.close("quit")
}
