package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/artifact"
	"github.com/ericflo/eagent/internal/protocol/control"
	"github.com/ericflo/eagent/internal/runtimecontrol"
	"github.com/ericflo/eagent/internal/settings"
	"github.com/ericflo/eagent/internal/store"
)

// StartConnector runs this project's settings connector. One process per
// project wins the election (every eagent process in the project tries); it
// publishes the project defaults and a live resource for each running
// session, and executes the commands the app queues for either.
func StartConnector(ctx context.Context, project string, logf func(string, ...any)) func() {
	return startConnectorAt(ctx, project, connectorPath(project), filepath.Join(stateDir(project), "connector.lock"), logf)
}

func readConnectorFile(path string, out any) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 96<<10 {
		return fmt.Errorf("connector state must be a bounded regular file")
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err = f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("connector state changed while opening")
	}
	raw, err := io.ReadAll(io.LimitReader(f, (96<<10)+1))
	if err != nil {
		return err
	}
	if len(raw) > 96<<10 {
		return fmt.Errorf("connector state exceeds limit")
	}
	return json.Unmarshal(raw, out)
}

func runtimeSnapshot(project, session string, grant control.Grant) (settings.RemoteView, error) {
	status, err := runtimecontrol.ReadStatus(project, session)
	if err != nil {
		return settings.RemoteView{}, err
	}
	view, err := runtimecontrol.View(project, session, status.Generation, status.Values, status.AvailableUntil.After(time.Now()))
	if err != nil {
		return view, err
	}
	if grant.Key != view.Key || grant.Scope != "session" {
		return view, fmt.Errorf("runtime grant identity changed")
	}
	canApply, preferences, routes := false, false, false
	for _, op := range grant.Operations {
		canApply = canApply || op == "settings.apply"
	}
	for _, class := range grant.Classes {
		preferences = preferences || class == "preference"
		routes = routes || class == "route"
	}
	if !canApply || (!preferences && !routes) {
		for i := range view.Descriptor.Fields {
			view.Descriptor.Fields[i].Writable = false
			view.Descriptor.Fields[i].LockedReason = "This connector has no approval to apply preferences."
		}
	}
	return view, nil
}

// maxSessionResources bounds how many running sessions get a live settings
// resource at once (the server caps a connector's grants at 50).
const maxSessionResources = 16

// runningSessions lists sessions in the project whose harness is alive right
// now, proven by a live runtime-status heartbeat. Newest first; only the
// newest 64 sessions are looked at, so a big session directory stays cheap.
func runningSessions(project string) []string {
	infos, err := store.List(store.Root(project))
	if err != nil {
		return nil
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].Started.After(infos[j].Started) })
	if len(infos) > 64 {
		infos = infos[:64]
	}
	var out []string
	for _, info := range infos {
		st, err := runtimecontrol.ReadStatus(project, info.ID)
		if err != nil || !st.AvailableUntil.After(time.Now()) {
			continue
		}
		out = append(out, info.ID)
	}
	return out
}

// sessionGrants is the grant set the project connector should hold: its
// project grant, any unrelated grant the server already issued, and one
// session grant per running session (newest first, bounded). Session grants
// of sessions that have stopped are dropped, so they never accumulate.
func sessionGrants(project string, held []control.Grant, projectGrant control.Grant, live []string) []control.Grant {
	out := []control.Grant{projectGrant}
	for _, g := range held {
		if g.Key != projectGrant.Key && !strings.HasPrefix(g.Key, projectGrant.Key+"-session-") {
			out = append(out, g)
		}
	}
	for _, sid := range live {
		if len(out) > maxSessionResources {
			break
		}
		if g, err := runtimecontrol.Grant(project, sid); err == nil {
			out = append(out, g)
		}
	}
	return out
}

// sameGrantKeys reports whether two grant sets name the same resources with
// the same operations and classes, ignoring order.
func sameGrantKeys(a, b []control.Grant) bool {
	if len(a) != len(b) {
		return false
	}
	index := map[string]string{}
	for _, g := range a {
		raw, _ := json.Marshal(g)
		index[g.Key] = string(raw)
	}
	for _, g := range b {
		raw, _ := json.Marshal(g)
		if index[g.Key] != string(raw) {
			return false
		}
	}
	return true
}

// sessionOfGrant returns the session a session grant of this project names.
func sessionOfGrant(projectKey string, g control.Grant) (string, bool) {
	if g.Scope != "session" {
		return "", false
	}
	sid, ok := strings.CutPrefix(g.Key, projectKey+"-session-")
	return sid, ok && sid != ""
}

// The connector records its intent before handing a command to the owner.
// A reclaimed intent may read an acknowledgement, but cannot enqueue again.
// The project editor lock protects only the shared audit append, never the wait.
func executeRuntimeCommand(ctx context.Context, project, session string, grant control.Grant, q command, reconcile bool) (commandJournal, error) {
	if !artifact.ValidPath(q.ID) || strings.Contains(q.ID, "/") || len(q.ID) > 80 {
		return commandJournal{}, fmt.Errorf("invalid command id")
	}
	path := filepath.Join(stateDir(project), "runtime-commands", q.ID+".json")
	journal := commandJournal{}
	existing := false
	if err := readConnectorFile(path, &journal); err == nil {
		existing = true
		if journal.CommandID != q.ID || journal.Digest != q.Digest || journal.ResourceKey != grant.Key {
			return journal, fmt.Errorf("runtime command identity changed")
		}
	} else if !os.IsNotExist(err) {
		return journal, err
	}
	if !existing {
		journal = commandJournal{CommandID: q.ID, Digest: q.Digest, ResourceKey: grant.Key, Status: "prepared"}
		if err := writeJSONAtomic(path, journal); err != nil {
			return journal, err
		}
	}
	if journal.Status == "prepared" {
		outcome, err := runtimecontrol.Deliver(ctx, project, session, runtimecontrol.Request{ID: q.ID, UserID: q.UserID, Digest: q.Digest, Expires: q.Expires, Grant: grant, Proposal: q.Proposal}, reconcile || existing)
		if err != nil {
			return journal, err
		}
		journal.Status, journal.Result = outcome.Status, outcome.Result
		if err := writeJSONAtomic(path, journal); err != nil {
			return journal, err
		}
	}
	if !journal.AuditRecorded {
		editor, err := config.LockEditor(ctx, project)
		if err != nil {
			return journal, err
		}
		defer editor.Close()
		if err := appendControlAudit(project, q, journal.Status, journal.Result); err != nil {
			return journal, err
		}
		journal.AuditRecorded = true
		if err := writeJSONAtomic(path, journal); err != nil {
			return journal, err
		}
	}
	return journal, nil
}
