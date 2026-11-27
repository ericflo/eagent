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
// The list is serve-hosted.json: {"sessions": [...], "at": ..., "configs":
// {session: {"config", "preset", "default"}}, "stop": [{"session": ...,
// "at": ..., "config": ..., "preset": ..., "default": ...}]}. "configs"
// names the configuration each session listed last ran on (runPick), which
// the next server brings it back on (runConfig). "stop" marks the sessions
// the user stopped while a server was stopping, whose stop had not been
// finished (its agents stopped and recorded) when the list was last
// written, each with when the mark was last made and the configuration the
// session last ran on (a mark or list from before these has none); a
// marked session is not among "sessions". The next server finishes such a
// stop, telling the session's phone by that configuration, and does not
// bring the session back; a mark the session's record shows a run begun
// after is just dropped. A mark stays on the list, through the next
// servers' starts and shutdowns, until its stop is finished or no longer
// holds. A list written before "stop" existed has none.

// hostedFile is where a stopping server lists the sessions it was hosting.
func hostedFile(project string) string {
	return filepath.Join(project, ".agents", "eagent", "serve-hosted.json")
}

type hostedList struct {
	Sessions []string   `json:"sessions"`
	At       time.Time  `json:"at"`
	Stop     []stopMark `json:"stop,omitempty"`
	// Configs names, by session, the configuration each of Sessions last
	// ran on (runPick).
	Configs map[string]runPick `json:"configs,omitempty"`
}

// stopMark is a stop the user asked for a listed session (hostedList).
type stopMark struct {
	Session string    `json:"session"`
	At      time.Time `json:"at"`
	// The configuration the session last ran on, on the server that made
	// the mark; none on a mark made before marks named it.
	runPick
}

// runPick names the configuration a run was built with, as loadConfig
// took it, this server's defaults applied and "-" for none, so that
// another server loads the same one; or, for a run started with none
// picked, says it ran on the server's own (Default), which a later run
// takes as that server's own. The zero value names none (not known).
type runPick struct {
	Config  string `json:"config,omitempty"`
	Preset  string `json:"preset,omitempty"`
	Default bool   `json:"default,omitempty"`
}

func (p runPick) String() string {
	if p.Default {
		return "the server's own configuration"
	}
	var parts []string
	if p.Config != "-" {
		parts = append(parts, "configuration "+p.Config)
	}
	if p.Preset != "-" {
		parts = append(parts, "preset "+p.Preset)
	}
	if len(parts) == 0 {
		return "the project's own configuration"
	}
	return strings.Join(parts, " with ")
}

// pickOf names the configuration loadConfig(bundle, preset) loads; with
// none picked, the server's own (Default).
func (s *Server) pickOf(bundle, preset string) runPick {
	if bundle == "" && preset == "" {
		return runPick{Default: true}
	}
	if bundle == "" {
		bundle = s.Bundle
	}
	if preset == "" {
		preset = s.Preset
	}
	if bundle == "" {
		bundle = "-"
	}
	if preset == "" {
		preset = "-"
	}
	return runPick{Config: bundle, Preset: preset}
}

// loadPick loads the configuration p names.
func (s *Server) loadPick(p runPick) (config.Config, error) {
	if p.Default {
		return s.loadConfig("", "")
	}
	return s.loadConfig(p.Config, p.Preset)
}

// noteRan notes the configuration a run of the session here is built
// with (ran).
func (s *Server) noteRan(id string, pick runPick) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ran == nil {
		s.ran = map[string]runPick{}
	}
	s.ran[id] = pick
}

// runConfig is the configuration the session runs on, wherever this
// server starts or speaks for it (host, for every resume with no
// configuration picked; a stop it finishes), and its names: the one its
// last run known here was built with (ran: a run here, or the list the
// previous server left); else the one the list of sessions to bring back
// names for it (its stop's mark, or its entry); else the one its record
// names (its session.start); else this server's own. The log says which,
// past the first, and why a named one was passed over.
func (s *Server) runConfig(info store.Info) (config.Config, runPick, error) {
	return s.configFor(info, nil)
}

// configFor is runConfig's walk over those sources, taking the first that
// loads and that accept, if given, takes (passing over, logged, one it
// does not); an error when none is taken.
func (s *Server) configFor(info store.Info, accept func(config.Config) (bool, string)) (config.Config, runPick, error) {
	type source struct {
		pick runPick
		from string // "": the last run known here
	}
	var sources []source
	s.mu.Lock()
	if p, known := s.ran[info.ID]; known {
		sources = append(sources, source{p, ""})
	} else {
		list, _ := s.readHosted()
		if m, ok := list.mark(info.ID); ok && m.runPick != (runPick{}) {
			sources = append(sources, source{m.runPick, "its stop's mark on the list of sessions to bring back"})
		} else if p, ok := list.Configs[info.ID]; ok && p != (runPick{}) {
			sources = append(sources, source{p, "the list of sessions to bring back"})
		}
	}
	s.mu.Unlock()
	if name := recordedConfig(info.Path); name != "" {
		sources = append(sources, source{runPick{Config: name, Preset: "-"}, "record"})
	}
	sources = append(sources, source{runPick{Default: true}, "own"})
	var last error
	for _, src := range sources {
		cfg, err := s.loadPick(src.pick)
		if err != nil {
			if src.from != "record" {
				s.logf("session %s: %s, which it last ran on, does not load (%v)", info.ID, src.pick, err)
			}
			last = err
			continue
		}
		if accept != nil {
			if ok, why := accept(cfg); !ok {
				s.logf("session %s: passing over %s: %s", info.ID, src.pick, why)
				last = errors.New(why)
				continue
			}
		}
		switch src.from {
		case "":
		case "record":
			s.logf("session %s runs on %s, the one its record names", info.ID, src.pick)
		case "own":
			s.logf("session %s runs on this server's own configuration: no other is known for it", info.ID)
		default:
			s.logf("session %s runs on %s, as %s names it", info.ID, src.pick, src.from)
		}
		return cfg, src.pick, nil
	}
	return config.Config{}, runPick{}, last
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
// is made again), naming the configuration its last run known here was
// built with (or, for one not known, the one the list names for it). Under
// mu.
func (s *Server) markStop(id string) {
	list, ok := s.readHosted()
	old, marked := list.mark(id)
	if !ok || !slices.Contains(list.Sessions, id) && !marked {
		return
	}
	m := stopMark{Session: id, At: time.Now().UTC(), runPick: old.runPick}
	if p, ok := list.Configs[id]; ok {
		m.runPick = p
	}
	if p, ranHere := s.ran[id]; ranHere {
		m.runPick = p
	}
	marks := append(slices.DeleteFunc(list.Stop, func(m stopMark) bool { return m.Session == id }), m)
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
// recorded, as a run's own stop does (harness.TellPhoneStopped).
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
// settled: not sent for one taken back, unconfirmed for the rest. Then,
// with the lock still held (no run of the session can come in between),
// the phone is told, if the record shows the session mirrored to it
// (mirrored), on the account of cfg, the configuration the session runs on
// (nil: none loads, and nothing is told). It returns who it stopped, and
// who it could not kill.
func stopAgents(path string, cfg *config.Config) (stopped, failed []string, err error) {
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
		how := "was stopped"
		if rel.State == "exited" {
			how = "exited" // as settleExited says it of an agent that exited
		}
		for _, d := range waiting {
			out := event.RelayDeliveredData{Name: rel.Name, ID: d.ID, OK: false, Outcome: event.DeliveryUnconfirmed,
				Detail: "the agent " + how + " before its arrival showed in its transcript, so whether it arrived cannot be confirmed"}
			if takenBack[d.ID] {
				out.Outcome, out.Detail = event.DeliveryNotSent, "it was held for the end of the agent's turn and taken back unread when the agent "+how
			}
			if _, err := sess.Append(event.New(event.RelayDelivered, event.ActorHarness, out)); err != nil {
				return stopped, failed, err
			}
		}
	}
	if cfg != nil && mirrored(evs) {
		// A note the sender is still owed, once posted, is recorded posted
		// here, in the record this holds, so no later run posts it again.
		tellPhoneStopped(*cfg, path, func(ev event.Event) { _, _ = sess.Append(ev) })
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

// sessionConfig is this server's own configuration, or nil when it cannot
// be loaded.
func (s *Server) sessionConfig() *config.Config {
	cfg, err := s.loadConfig("", "")
	if err != nil {
		return nil
	}
	return &cfg
}

// stopConfig is the configuration a stop this server finishes tells the
// session's phone by: the first of runConfig's that has an account on the
// phone service the record shows the session's thread on (its latest
// phone.thread; any, for a record that names none), or nil when none
// does: nothing is told, and the log says why.
func (s *Server) stopConfig(info store.Info) *config.Config {
	var accept func(config.Config) (bool, string)
	if base := threadBase(info.Path); base != "" {
		accept = accountAt(base)
	}
	cfg, _, err := s.configFor(info, accept)
	if err != nil {
		s.logf("session %s: its phone is not told of the stop: no configuration known for it has an account where its thread is (%v)", info.ID, err)
		return nil
	}
	return &cfg
}

// accountAt takes a configuration whose phone account is on the service
// at base.
func accountAt(base string) func(config.Config) (bool, string) {
	return func(cfg config.Config) (bool, string) {
		c, ok := finalechat.Resolve(cfg.Finalechat.TokenEnvName(), cfg.Finalechat.BaseURL)
		if !ok {
			return false, "it has no phone account (no token)"
		}
		if strings.TrimRight(c.BaseURL, "/") != strings.TrimRight(base, "/") {
			return false, "its phone account is at " + c.BaseURL + ", and the session's thread at " + base
		}
		return true, ""
	}
}

// phoneOn is cfg, the configuration a session runs on, for a resume for a
// reply from the phone: the mirror on, on the account the record shows
// the session's thread on (the phone section of the first configuration
// runConfig's walk finds with an account there; cfg's own when the record
// names no address; this server's own, which the reply watcher read the
// reply on, when none has one there), so the reply is answered in its
// thread. The model and the rest stay cfg's.
func (s *Server) phoneOn(info store.Info, cfg config.Config) config.Config {
	if base := threadBase(info.Path); base != "" {
		if acc, _, err := s.configFor(info, accountAt(base)); err == nil {
			cfg.Finalechat = acc.Finalechat
		} else if own := s.sessionConfig(); own != nil {
			s.logf("session %s: no configuration known for it has an account where its thread is (%v); the reply is answered on this server's own account", info.ID, err)
			cfg.Finalechat = own.Finalechat
		}
	}
	on := true
	cfg.Finalechat.Enabled = &on
	return cfg
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

// recordedConfig is the configuration (bundle or preset) the session's
// record names in its session.start, "" for none.
func recordedConfig(path string) string {
	evs, err := store.Read(path)
	if err != nil {
		return ""
	}
	for _, ev := range evs {
		var d event.SessionStartData
		if ev.Type == event.SessionStart && ev.Decode(&d) == nil {
			return d.Config
		}
	}
	return ""
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

// writeHostedList writes the list with the sessions whose stop is asked,
// naming for each session listed the configuration it last ran on: the
// one known here (ran), else the one the list it replaces named. Under mu.
func (s *Server) writeHostedList(ids []string, at time.Time, stop []stopMark) {
	path := hostedFile(s.Project)
	if len(ids) == 0 && len(stop) == 0 {
		_ = os.Remove(path)
		return
	}
	prev, _ := s.readHosted()
	var configs map[string]runPick
	for _, id := range ids {
		p, ok := s.ran[id]
		if !ok {
			p, ok = prev.Configs[id]
		}
		if ok {
			if configs == nil {
				configs = map[string]runPick{}
			}
			configs[id] = p
		}
	}
	raw, _ := json.MarshalIndent(hostedList{Sessions: ids, At: at, Stop: stop, Configs: configs}, "", "  ")
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

// pendingReply finds that reply; tests substitute it. Left nil, the restore
// reads it on this server's own phone (integration.PendingReplyIn).
var pendingReply func(ctx context.Context, project string, info store.Info) (integration.PhoneMessage, bool, error)

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
	// a cut partway, leaves it for the next start. The configuration each
	// session listed last ran on is known here from now (ran): it comes
	// back on it (runConfig).
	s.mu.Lock()
	for id, p := range list.Configs {
		if _, known := s.ran[id]; !known && slices.Contains(list.Sessions, id) {
			if s.ran == nil {
				s.ran = map[string]runPick{}
			}
			s.ran[id] = p
		}
	}
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
		if stopped, failed, err := stopAgents(info.Path, s.stopConfig(info)); err != nil {
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
	// The reply that waited is read on the phone the sessions this server
	// resumes run with (its bundle and preset over the project's), and none
	// is taken when that phone is off, as their mirror takes none.
	takeReply := pendingReply
	if takeReply == nil {
		fc, err := s.PhoneConfig()
		takeReply = func(ctx context.Context, project string, info store.Info) (integration.PhoneMessage, bool, error) {
			if err != nil {
				return integration.PhoneMessage{}, false, err
			}
			return integration.PendingReplyIn(ctx, fc, project, info)
		}
	}
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
		reply, _, err := takeReply(ctx, s.Project, info)
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
