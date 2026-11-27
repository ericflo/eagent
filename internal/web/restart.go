package web

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/finalechat"
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
// [...]}. "stop" names the listed sessions the user stopped while the
// server was stopping, whose stop had not been finished (its agents
// stopped and recorded) when the list was last written: the next server
// finishes it (finishStop) and does not bring the session back. A list
// written before "stop" existed has none.

// hostedFile is where a stopping server lists the sessions it was hosting.
func hostedFile(project string) string {
	return filepath.Join(project, ".agents", "eagent", "serve-hosted.json")
}

type hostedList struct {
	Sessions []string  `json:"sessions"`
	At       time.Time `json:"at"`
	Stop     []string  `json:"stop,omitempty"`
}

// rememberHosted lists the sessions this server hosts, before they stop.
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
	ids := make([]string, 0, len(s.running))
	for id := range s.running {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	s.writeHosted(ids, time.Now().UTC())
}

// forgetHosted takes a session the user stopped off the list, so the next
// server does not bring it back. Under mu.
func (s *Server) forgetHosted(id string) {
	list, ok := s.readHosted()
	if !ok || !slices.Contains(list.Sessions, id) {
		return
	}
	list.Stop = slices.DeleteFunc(list.Stop, func(x string) bool { return x == id })
	s.writeHostedList(slices.DeleteFunc(list.Sessions, func(x string) bool { return x == id }), list.At, list.Stop)
}

// markStop notes on the list that the user stopped a listed session, so
// that the next server finishes the stop rather than bring it back, should
// this one exit before it has. Under mu.
func (s *Server) markStop(id string) {
	list, ok := s.readHosted()
	if !ok || !slices.Contains(list.Sessions, id) || slices.Contains(list.Stop, id) {
		return
	}
	s.writeHostedList(list.Sessions, list.At, append(list.Stop, id))
}

// readHosted reads the list. Under mu.
func (s *Server) readHosted() (hostedList, bool) {
	raw, err := os.ReadFile(hostedFile(s.Project))
	var list hostedList
	return list, err == nil && json.Unmarshal(raw, &list) == nil
}

// listed reports whether the list names the session. Under mu.
func (s *Server) listed(id string) bool {
	list, ok := s.readHosted()
	return ok && slices.Contains(list.Sessions, id)
}

// killAgent stops an agent's terminal; tests replace it.
var killAgent = func(host, target string) error { return relay.HostByName(host).Kill(target) }

// stopAgents stops the agents the record of a session nobody runs shows
// running, as a stop the session takes does (stopSession), under the
// session's lock and from the record as it then stands: for each one, a
// message still unclaimed in its spool is taken back first (it did not
// go), its terminal is killed (not that of one the record shows exited:
// it is gone, and nothing is said of it), its relay.end is recorded (one
// that could not be killed too, as stopSession does), and its sends are
// settled: not sent for one taken back, unconfirmed for the rest. It
// returns who it stopped, and who it could not kill.
func stopAgents(path string) (stopped, failed []string, err error) {
	sess, evs, err := store.Open(path, time.Now())
	if err != nil {
		return nil, nil, err
	}
	defer sess.Close()
	for _, rel := range state.Replay(evs).ActiveRelays() {
		waiting := unsettled(evs, rel)
		spool := relaySpool(path, rel)
		takenBack := map[string]bool{}
		for _, d := range waiting {
			if d.Mode == "after_turn" && spool != "" && relay.Unspool(spool, d.ID) {
				takenBack[d.ID] = true
			}
		}
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
		for _, d := range waiting {
			out := event.RelayDeliveredData{Name: rel.Name, ID: d.ID, OK: false, Outcome: event.DeliveryUnconfirmed,
				Detail: "the agent was stopped before its arrival showed in its transcript, so whether it arrived cannot be confirmed"}
			if takenBack[d.ID] {
				out.Outcome, out.Detail = event.DeliveryNotSent, "it was held for the end of the agent's turn and taken back unread when the agent was stopped"
			}
			if _, err := sess.Append(event.New(event.RelayDelivered, event.ActorHarness, out)); err != nil {
				return stopped, failed, err
			}
		}
	}
	return stopped, failed, nil
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
func (s *Server) writeHostedList(ids []string, at time.Time, stop []string) {
	path := hostedFile(s.Project)
	if len(ids) == 0 {
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
	_ = os.Remove(path)
	var list hostedList
	if err := json.Unmarshal(raw, &list); err != nil {
		s.logf("could not read the sessions to bring back: %v", err)
		return
	}
	// Reading the threads waits on the network; it gets a bounded time,
	// and a session whose thread cannot be read is left to the reply
	// watcher rather than brought back without the reply it holds.
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	back := 0
	for _, id := range list.Sessions {
		info, err := store.Resolve(store.Root(s.Project), id)
		if err != nil || info.ID != id {
			s.logf("session %s is gone; not bringing it back", id)
			continue
		}
		if alive(info.Path) || s.isHosted(id) {
			continue
		}
		if slices.Contains(list.Stop, id) {
			// The user stopped it while the last server was stopping,
			// which exited before the stop was done: it is finished here,
			// and the session stays stopped.
			if stopped, failed, err := stopAgents(info.Path); err != nil {
				s.logf("session %s: could not finish the stop asked before the restart: %v", id, err)
			} else {
				s.logf("session %s: finished the stop asked before the restart (stopped %d agent(s), could not kill %d)", id, len(stopped), len(failed))
			}
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
