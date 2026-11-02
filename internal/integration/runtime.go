package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/finalechat"
	"github.com/ericflo/eagent/internal/protocol/artifact"
	"github.com/ericflo/eagent/internal/protocol/control"
	"github.com/ericflo/eagent/internal/runtimecontrol"
	"github.com/ericflo/eagent/internal/settings"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/store"
)

func runtimeConnectorDir(project string) string {
	return filepath.Join(stateDir(project), "runtime-connectors")
}

// PairSession requests an additional, explicit session grant. A project-default
// connector never acquires permission to change a running session implicitly.
func PairSession(ctx context.Context, project, ref string) (string, error) {
	if finalechat.Disabled() {
		return "", finalechat.ErrDisabled
	}
	info, err := store.Resolve(store.Root(project), ref)
	if err != nil {
		return "", err
	}
	grant, err := runtimecontrol.Grant(project, info.ID)
	if err != nil {
		return "", err
	}
	return pairResource(ctx, project, "eagent · "+grant.Label, info.ID, filepath.Join(runtimeConnectorDir(project), info.ID+".json"), grant)
}

// StartConnector supervises independent elected workers for project defaults
// and explicitly paired sessions. Pairing during a run is discovered locally.
func StartConnector(ctx context.Context, project string, logf func(string, ...any)) func() {
	if finalechat.Disabled() {
		return func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		projectStop := startConnectorAt(ctx, project, "", connectorPath(project), filepath.Join(stateDir(project), "connector.lock"), logf)
		defer projectStop()
		workers := map[string]func(){}
		defer func() {
			for _, stop := range workers {
				stop()
			}
		}()
		for ctx.Err() == nil {
			// Bound discovery even if local files are accidentally generated in a loop.
			if dir, err := os.Open(runtimeConnectorDir(project)); err == nil {
				entries, readErr := dir.ReadDir(257)
				dir.Close()
				if (readErr == nil || readErr == io.EOF) && len(entries) <= 256 {
					present := map[string]bool{}
					for _, entry := range entries {
						session, ok := strings.CutSuffix(entry.Name(), ".json")
						if !ok || !entry.Type().IsRegular() {
							continue
						}
						if _, err := runtimecontrol.Grant(project, session); err != nil {
							continue
						}
						present[session] = true
						if workers[session] == nil {
							workers[session] = startConnectorAt(ctx, project, session, filepath.Join(runtimeConnectorDir(project), entry.Name()), filepath.Join(runtimeConnectorDir(project), session+".lock"), logf)
						}
					}
					for session, stop := range workers {
						if !present[session] {
							stop()
							delete(workers, session)
						}
					}
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
	}()
	return func() { cancel(); <-done }
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
	// Carry the routes this session is actually running (per actor: model,
	// host, reasoning effort — after any fallback) so the phone can show the
	// real picks, not the preset name the project file happens to name.
	if routes, cfgName, ok := sessionRoutes(project, session); ok {
		view.Snapshot.Details["routes"] = routes
		if cfgName != "" {
			view.Snapshot.Details["config"] = cfgName
		}
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

// sessionRoutes replays a session's log and returns the per-actor routes in
// use (model, host, effort) plus the preset/bundle name, for display in the
// session-scoped settings snapshot.
func sessionRoutes(project, session string) (map[string]any, string, bool) {
	info, err := store.Resolve(store.Root(project), session)
	if err != nil {
		return nil, "", false
	}
	evs, err := store.Read(info.Path)
	if err != nil {
		return nil, "", false
	}
	st := state.Replay(evs)
	if len(st.Models) == 0 {
		return nil, "", false
	}
	routes := map[string]any{}
	for actor, model := range st.Models {
		routes[actor] = map[string]any{
			"model":  model,
			"host":   st.Hosts[actor],
			"effort": st.Efforts[actor],
		}
	}
	return routes, st.Config, true
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
