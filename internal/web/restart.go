package web

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/finalechat"
	"github.com/ericflo/eagent/internal/harness"
	"github.com/ericflo/eagent/internal/integration"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/store"
)

// A server that stops writes down the sessions it was hosting, and the next
// one brings them back at once, so restarting eagent does not leave every
// thread quiet until the user writes to it. A relay session picks its agent
// up again on resume; the agents themselves run in terminals of their own
// and never stopped. A hard kill writes nothing: those sessions come back on
// the user's next message, as before.
//
// The list is serve-hosted.json: {"sessions": [...], "at": ..., "stop":
// [{"session": ..., "at": ...}]}. "stop" marks the sessions the user
// stopped while a server was stopping, whose stop had not been finished
// (its agents stopped and recorded) when the list was last written, each
// with when the mark was last made; a marked session is not among
// "sessions". The next server finishes such a stop and does not bring the
// session back; a mark the session's record shows a run begun after is
// just dropped. A mark stays on the list, through the next servers' starts
// and shutdowns, until its stop is finished or no longer holds. A list
// written before "stop" existed has none. (Fields a list or mark was
// written with that are no longer used, such as "configs", are ignored.)

// hostedFile is where a stopping server lists the sessions it was hosting.
func hostedFile(project string) string {
	return filepath.Join(project, ".agents", "eagent", "serve-hosted.json")
}

type hostedList struct {
	Sessions []string   `json:"sessions"`
	At       time.Time  `json:"at"`
	Stop     []stopMark `json:"stop,omitempty"`
}

// stopMark is a stop the user asked for a listed session (hostedList).
type stopMark struct {
	Session string    `json:"session"`
	At      time.Time `json:"at"`
}

// mark is the list's mark for the session, if it has one.
func (l hostedList) mark(id string) (stopMark, bool) {
	for _, m := range l.Stop {
		if m.Session == id {
			return m, true
		}
	}
	return stopMark{}, false
}

// rememberHosted lists the sessions this server hosts, before they stop,
// beside the marks already on the list (a stop a start could not finish).
// It sets the shutdown mark first, under the lock every start holds, so no
// run starts after the list is taken (host, hostNewCfg). One the user
// stops after that is taken off again (forgetHosted): the list is written
// under mu, as a run's end is recorded.
func (s *Server) rememberHosted() {
	s.resuming.Lock()
	defer s.resuming.Unlock()
	s.stopping = true
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, _ := s.readHosted()
	ids := make([]string, 0, len(s.running))
	for id := range s.running {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	s.writeHostedList(ids, time.Now().UTC(), prev.Stop)
}

// forgetHosted takes a session the user stopped off the list, so the next
// server does not bring it back, and its mark with it. Under mu.
func (s *Server) forgetHosted(id string) {
	list, ok := s.readHosted()
	if _, marked := list.mark(id); !ok || !slices.Contains(list.Sessions, id) && !marked {
		return
	}
	list.Stop = slices.DeleteFunc(list.Stop, func(m stopMark) bool { return m.Session == id })
	s.writeHostedList(slices.DeleteFunc(list.Sessions, func(x string) bool { return x == id }), list.At, list.Stop)
}

// markStop notes on the list that the user stopped a listed (or marked)
// session, so that the next server finishes the stop rather than bring it
// back, should this one exit before it has: the session moves from the
// sessions to bring back to the marks, its mark made now (a mark it had
// is made again). Under mu.
func (s *Server) markStop(id string) {
	list, ok := s.readHosted()
	if _, marked := list.mark(id); !ok || !slices.Contains(list.Sessions, id) && !marked {
		return
	}
	marks := append(slices.DeleteFunc(list.Stop, func(m stopMark) bool { return m.Session == id }), stopMark{Session: id, At: time.Now().UTC()})
	s.writeHostedList(slices.DeleteFunc(list.Sessions, func(x string) bool { return x == id }), list.At, marks)
}

// readHosted reads the list. Under mu.
func (s *Server) readHosted() (hostedList, bool) {
	raw, err := os.ReadFile(hostedFile(s.Project))
	var list hostedList
	return list, err == nil && json.Unmarshal(raw, &list) == nil
}

// listed reports whether the list names the session, to bring back or as
// marked. Under mu.
func (s *Server) listed(id string) bool {
	list, ok := s.readHosted()
	_, marked := list.mark(id)
	return ok && (slices.Contains(list.Sessions, id) || marked)
}

// tellPhoneStopped tells the session's phone thread of a stop stopAgents
// recorded, as a run's own stop does (harness.TellPhoneStopped), or says
// why it told nothing.
var tellPhoneStopped = harness.TellPhoneStopped

// killAgent stops an agent's terminal; tests replace it.
var killAgent = func(host, target string) error { return relay.HostByName(host).Kill(target) }

// stopAgents stops the agents the record of a session nobody runs shows
// running, as a stop the session takes does (stopSession), under the
// session's lock and from the record as it then stands: for each one, a
// message still unclaimed in its spool is taken back first (it did not
// go), its terminal is killed (not that of one the record shows exited:
// it is gone, and nothing is said of it), its relay.end is recorded (one
// that could not be killed too, as stopSession does), and its sends are
// settled: not sent for one taken back, unconfirmed for the rest. Before
// that, what the record still waits on with no agent behind it is settled
// as a resume settles it, by the same rules: not sent for a send held for
// a start that never came up (state.NeverTyped), and the sends of an agent
// stopped before (harness.EndedWaiting). Then,
// with the lock still held (no run of the session can come in between),
// the phone is told, if the record shows the session mirrored to it
// (mirrored), on the account of cfgs that has the session's thread
// (stopConfigs; none: nothing is told); logf says why nothing was told.
// Told or not, the thread is left with project's reply watcher, as a
// run's own mirror leaves it when it stops (integration.LeftForNextRun):
// the closing note hides the thread from the watcher's sweep, this stop's
// writes make the record newer than a reply sent while nothing watched
// (eagent serve was down, and this is the next one finishing the stop),
// and that reply is still to be found.
// It returns who it stopped, and who it could not kill.
func stopAgents(project, path string, cfgs []config.Config, logf func(string, ...any)) (stopped, failed []string, err error) {
	sess, evs, err := store.Open(path, time.Now())
	if err != nil {
		return nil, nil, err
	}
	defer sess.Close()
	st := state.Replay(evs)
	// What is settled here, or is to stay as it is.
	done := map[string]bool{}
	// settle records how the sends the record still waits on for rel went,
	// its agent being gone (how): one held for the end of its turn and
	// still unread in the spool was taken back (takenBack) and did not go;
	// of the rest nothing can be confirmed.
	settle := func(rel *state.Relay, waiting []event.RelaySendData, takenBack map[string]bool, how string) error {
		for _, d := range waiting {
			if done[d.ID] {
				continue
			}
			out := event.RelayDeliveredData{Name: rel.Name, ID: d.ID, OK: false, Outcome: event.DeliveryUnconfirmed,
				Detail: "the agent " + how + " before its arrival showed in its transcript, so whether it arrived cannot be confirmed"}
			if takenBack[d.ID] {
				out.Outcome, out.Detail = event.DeliveryNotSent, "it was held for the end of the agent's turn and taken back unread when the agent "+how
			}
			if _, err := sess.Append(event.New(event.RelayDelivered, event.ActorHarness, out)); err != nil {
				return err
			}
		}
		return nil
	}
	takeBack := func(rel *state.Relay, waiting []event.RelaySendData) map[string]bool {
		spool := relaySpool(path, rel)
		taken := map[string]bool{}
		for _, d := range waiting {
			if !done[d.ID] && d.Mode == "after_turn" && spool != "" && relay.Unspool(spool, d.ID) {
				taken[d.ID] = true
			}
		}
		return taken
	}
	// As a resume settles a record with no run behind it (harness
	// reattachRelays), whatever became of each agent: what was held for a
	// start that never came up was never typed, and what a stopped agent
	// is still waited on for (its stop's settle was cut) is settled as
	// that stop would have settled it.
	for _, d := range state.NeverTyped(evs) {
		if _, err := sess.Append(event.New(event.RelayDelivered, event.ActorHarness, state.NotTyped(d))); err != nil {
			return nil, nil, err
		}
		done[d.ID] = true
	}
	ended, keep := harness.EndedWaiting(st)
	for _, id := range keep {
		done[id] = true
	}
	for _, rel := range ended {
		waiting := unsettled(evs, rel)
		if err := settle(rel, waiting, takeBack(rel, waiting), "was stopped"); err != nil {
			return nil, nil, err
		}
	}
	for _, rel := range st.ActiveRelays() {
		waiting := unsettled(evs, rel)
		takenBack := takeBack(rel, waiting)
		if rel.State != "exited" {
			if rel.Target != "" {
				if kerr := killAgent(rel.Host, rel.Target); kerr != nil {
					failed = append(failed, rel.Who()+" ("+kerr.Error()+")")
				}
			}
			stopped = append(stopped, rel.Who())
		}
		if _, err := sess.Append(event.New(event.RelayEnd, event.ActorHarness, event.RelayEndData{Name: rel.Name, Reason: "the user stopped the session"})); err != nil {
			return stopped, failed, err
		}
		how := "was stopped"
		if rel.State == "exited" {
			how = "exited" // as settleExited says it of an agent that exited
		}
		if err := settle(rel, waiting, takenBack, how); err != nil {
			return stopped, failed, err
		}
	}
	if len(cfgs) > 0 && mirrored(evs) {
		// A note the sender is still owed, once posted, is recorded posted
		// here, in the record this holds, so no later run posts it again.
		if logf == nil {
			logf = func(string, ...any) {}
		}
		if err := tellPhoneStopped(cfgs, path, func(ev event.Event) { _, _ = sess.Append(ev) }); err != nil {
			logf("session %s: its phone is not told of the stop: %v", filepath.Base(path), err)
		}
		// Told or not: this stop's own writes make the record newer than
		// a reply that waits in the thread, and the sweep passes over a
		// thread no newer than its record.
		integration.LeftForNextRun(project, filepath.Base(path), true, logf)
	}
	return stopped, failed, nil
}

// mirrored reports whether a session's record shows it mirrored to the
// phone: a phone.* event (phone.thread, once its mirror began). Whether a
// stop tells a phone at all is read from this; the configuration gives
// only the account. The phone_ledger flag on session.start and
// session.resume does not show it: every run writes it, the phone on or
// off.
func mirrored(evs []event.Event) bool {
	for _, ev := range evs {
		if strings.HasPrefix(ev.Type, "phone.") {
			return true
		}
	}
	return false
}

// stopConfigs are the configurations on whose phone account a stop this
// server finishes may tell the session's phone (whether it tells at all is
// the record's: stopAgents): the project's file, then this server's own,
// each of them whose account is at the address the record shows the
// session's thread on (its latest phone.thread; addresses compared with
// integration.AccountOf), one account once; for a record that names no
// address, the project's file (this server's own when that cannot be
// loaded). Two of them may name one service with different tokens: the
// stop tells on the one that has the thread (harness.TellPhoneStopped).
// With none, nothing is told, and the log says why.
func (s *Server) stopConfigs(info store.Info) []config.Config {
	base := threadBase(info.Path)
	file, fileErr := config.Load(s.Project, "")
	own, ownErr := s.loadConfig("", "")
	var out []config.Config
	seen := map[string]bool{}
	for _, c := range []struct {
		cfg config.Config
		err error
	}{{file, fileErr}, {own, ownErr}} {
		if c.err != nil {
			continue
		}
		if base == "" {
			return []config.Config{c.cfg}
		}
		client, ok := finalechat.Resolve(c.cfg.Finalechat.TokenEnvName(), c.cfg.Finalechat.BaseURL)
		if !ok || integration.AccountOf(client.BaseURL) != integration.AccountOf(base) || seen[client.Token] {
			continue
		}
		seen[client.Token] = true
		out = append(out, c.cfg)
	}
	if len(out) == 0 {
		s.logf("session %s: its phone is not told of the stop: neither the project's file nor this server's configuration has a phone account at %s, where its thread is", info.ID, base)
	}
	return out
}

// threadBase is the address of the phone service the session's record
// shows its thread on (its latest phone.thread), "" for none recorded.
func threadBase(path string) string {
	evs, err := store.Read(path)
	if err != nil {
		return ""
	}
	base := ""
	for _, ev := range evs {
		var d event.PhoneThreadData
		if ev.Type == event.PhoneThread && ev.Decode(&d) == nil && d.BaseURL != "" {
			base = d.BaseURL
		}
	}
	return base
}

// unsettled lists the sends the record still waits on for rel (sent, no
// outcome), in the order they were sent, each as its latest relay.send or
// relay.moved has it, as the harness's unresolvedSends.
func unsettled(evs []event.Event, rel *state.Relay) []event.RelaySendData {
	var out []event.RelaySendData
	at := map[string]int{}
	for _, ev := range evs {
		var d event.RelaySendData
		if ev.Type != event.RelaySend && ev.Type != event.RelayMoved || ev.Decode(&d) != nil || d.Name != rel.Name {
			continue
		}
		if _, waiting := rel.Undelivered[d.ID]; !waiting {
			continue
		}
		if i, seen := at[d.ID]; seen {
			out[i] = d
			continue
		}
		at[d.ID] = len(out)
		out = append(out, d)
	}
	return out
}

// relaySpool is where a message held for the end of rel's turn waits, as
// the harness's relaySpec has it; "" for an agent with none.
func relaySpool(path string, rel *state.Relay) string {
	spec := relay.Spec{Profile: config.RelayProfile{Agent: rel.Agent}}
	if relay.Prepare(&spec, filepath.Join(path, "relays", rel.Name)) != nil {
		return ""
	}
	return spec.Spool
}

// writeHosted writes the list of sessions to bring back.
func (s *Server) writeHosted(ids []string, at time.Time) { s.writeHostedList(ids, at, nil) }

// writeHostedList writes the list with the sessions whose stop is asked.
func (s *Server) writeHostedList(ids []string, at time.Time, stop []stopMark) {
	path := hostedFile(s.Project)
	if len(ids) == 0 && len(stop) == 0 {
		_ = os.Remove(path)
		return
	}
	raw, _ := json.MarshalIndent(hostedList{Sessions: ids, At: at, Stop: stop}, "", "  ")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o644); err != nil {
		s.logf("could not note the running sessions for the next start: %v", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		s.logf("could not note the running sessions for the next start: %v", err)
	}
}

// ranSince reports whether the session's record shows a run that began
// after at (a session.resume written later).
func ranSince(path string, at time.Time) bool {
	evs, err := store.Read(path)
	if err != nil {
		return false
	}
	for _, ev := range evs {
		if ev.Type == event.SessionResume && ev.Time.After(at) {
			return true
		}
	}
	return false
}

// restoreSession resumes one session, with the reply that came while it was
// down if there is one; tests substitute it.
var restoreSession = func(s *Server, id string, reply integration.PhoneMessage) error { return s.Resume(id, reply) }

// pendingReply finds that reply; tests substitute it.
var pendingReply = integration.PendingReply

// releaseReply gives it back to the reply watcher when the session could
// not come back with it; tests substitute it.
var releaseReply = integration.ReleaseReply

// restoreHosted brings back the sessions the previous server was hosting
// when it stopped. The list is used once: a session that cannot come back
// is not tried again at every start.
func (s *Server) restoreHosted(ctx context.Context) {
	path := hostedFile(s.Project)
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var list hostedList
	if err := json.Unmarshal(raw, &list); err != nil {
		_ = os.Remove(path)
		s.logf("could not read the sessions to bring back: %v", err)
		return
	}
	// The list is used once, but a marked stop stays on it until it is
	// finished or no longer holds (forgetHosted below): one that fails, or
	// a cut partway, leaves it for the next start.
	s.mu.Lock()
	s.writeHostedList(nil, list.At, list.Stop)
	s.mu.Unlock()
	forget := func(id string) {
		s.mu.Lock()
		s.forgetHosted(id)
		s.mu.Unlock()
	}
	// The marks first. The user stopped each of these while a server was
	// stopping, which exited before the stop was done: it is finished
	// here, and the session stays stopped. A mark the session has run
	// since (resumed elsewhere after the mark was made) no longer says
	// what the user wants of it, and is just dropped.
	stopping := map[string]bool{}
	for _, m := range list.Stop {
		id := m.Session
		info, err := store.Resolve(store.Root(s.Project), id)
		if err != nil || info.ID != id {
			s.logf("session %s is gone; not finishing the stop asked for it", id)
			forget(id)
			continue
		}
		if ranSince(info.Path, m.At) {
			s.logf("session %s: the stop asked before the restart no longer holds: the session has run since", id)
			forget(id)
			continue
		}
		stopping[id] = true
		if alive(info.Path) || s.isHosted(id) {
			continue
		}
		if stopped, failed, err := stopAgents(s.Project, info.Path, s.stopConfigs(info), s.logf); err != nil {
			s.logf("session %s: could not finish the stop asked before the restart (left marked on the list): %v", id, err)
		} else {
			s.logf("session %s: finished the stop asked before the restart (stopped %d agent(s), could not kill %d)", id, len(stopped), len(failed))
			forget(id)
		}
	}
	// Reading the threads waits on the network; it gets a bounded time,
	// and a session whose thread cannot be read is left to the reply
	// watcher rather than brought back without the reply it holds.
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	back := 0
	for _, id := range list.Sessions {
		if stopping[id] {
			continue
		}
		info, err := store.Resolve(store.Root(s.Project), id)
		if err != nil || info.ID != id {
			s.logf("session %s is gone; not bringing it back", id)
			continue
		}
		if alive(info.Path) || s.isHosted(id) {
			continue
		}
		reply, _, err := pendingReply(ctx, s.Project, info)
		if finalechat.Gone(err) {
			// FinaleChat says the session has no thread there, so no reply
			// can be waiting in one: it comes back as it was.
			reply, err = integration.PhoneMessage{}, nil
		}
		if errors.Is(err, integration.ErrHandOver) {
			// Its thread was read: the reply stays there, untaken.
			s.logf("session %s: not brought back (%v); the reply stays in its thread", id, err)
			continue
		}
		if err != nil {
			s.logf("session %s: could not check its thread for a reply (%v); it comes back on your next message", id, err)
			continue
		}
		if err := restoreSession(s, id, reply); err != nil {
			s.logf("session %s could not be brought back: %v", id, err)
			if err := releaseReply(s.Project, info, reply); err != nil {
				s.logf("session %s: its reply waits for your next message (%v)", id, err)
			}
			continue
		}
		back++
	}
	if back > 0 {
		s.logf("brought back %d session(s) that were running before the restart", back)
	}
}
