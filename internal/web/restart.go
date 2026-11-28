package web

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"time"

	"github.com/ericflo/eagent/internal/finalechat"
	"github.com/ericflo/eagent/internal/integration"
	"github.com/ericflo/eagent/internal/store"
)

// A server that stops writes down the sessions it was hosting, and the next
// one brings them back at once, so restarting eagent does not leave every
// thread quiet until the user writes to it. A relay session picks its agent
// up again on resume; the agents themselves run in terminals of their own
// and never stopped. A hard kill writes nothing: those sessions come back on
// the user's next message, as before.

// hostedFile is where a stopping server lists the sessions it was hosting.
func hostedFile(project string) string {
	return filepath.Join(project, ".agents", "eagent", "serve-hosted.json")
}

type hostedList struct {
	Sessions []string  `json:"sessions"`
	At       time.Time `json:"at"`
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
	raw, err := os.ReadFile(hostedFile(s.Project))
	var list hostedList
	if err != nil || json.Unmarshal(raw, &list) != nil || !slices.Contains(list.Sessions, id) {
		return
	}
	s.writeHosted(slices.DeleteFunc(list.Sessions, func(x string) bool { return x == id }), list.At)
}

// writeHosted writes the list of sessions to bring back.
func (s *Server) writeHosted(ids []string, at time.Time) {
	path := hostedFile(s.Project)
	if len(ids) == 0 {
		_ = os.Remove(path)
		return
	}
	raw, _ := json.MarshalIndent(hostedList{Sessions: ids, At: at}, "", "  ")
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
		reply, _, err := pendingReply(ctx, s.Project, info)
		if finalechat.Gone(err) {
			// FinaleChat says the session has no thread there, so no reply
			// can be waiting in one: it comes back as it was.
			reply, err = integration.PhoneMessage{}, nil
		}
		if err != nil {
			s.logf("session %s: could not check its thread for a reply (%v); it comes back on your next message", id, err)
			continue
		}
		if err := restoreSession(s, id, reply); err != nil {
			s.logf("session %s could not be brought back: %v", id, err)
			continue
		}
		back++
	}
	if back > 0 {
		s.logf("brought back %d session(s) that were running before the restart", back)
	}
}
