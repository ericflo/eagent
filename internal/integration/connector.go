package integration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/filelock"
	"github.com/ericflo/eagent/internal/finalechat"
	"github.com/ericflo/eagent/internal/protocol/artifact"
	"github.com/ericflo/eagent/internal/protocol/control"
	"github.com/ericflo/eagent/internal/runtimecontrol"
	"github.com/ericflo/eagent/internal/settings"
)

type connectorConfig struct {
	ID          string `json:"id"`
	Secret      string `json:"secret"`
	BaseURL     string `json:"base_url"`
	ApprovalURL string `json:"approval_url"`
	Session     string `json:"session_id,omitempty"`
}
type connectorInfo struct {
	ID     string          `json:"id"`
	State  string          `json:"state"`
	Grants []control.Grant `json:"grants"`
}
type resourceInfo struct {
	ID         string `json:"id"`
	Key        string `json:"key"`
	Generation string `json:"generation"`
}
type command struct {
	ID         string           `json:"id"`
	UserID     string           `json:"user_id"`
	ResourceID string           `json:"resource_id"`
	Proposal   control.Proposal `json:"proposal"`
	Digest     string           `json:"proposal_sha256"`
	ClaimToken string           `json:"claim_token"`
	Expires    time.Time        `json:"expires_at"`
	Attempts   int              `json:"attempts"`
}
type commandJournal struct {
	CommandID     string                     `json:"command_id"`
	Digest        string                     `json:"proposal_sha256"`
	ResourceKey   string                     `json:"resource_key"`
	BeforeETag    string                     `json:"before_etag"`
	Desired       string                     `json:"desired,omitempty"`
	DesiredETag   string                     `json:"desired_etag,omitempty"`
	Status        string                     `json:"status"`
	Result        map[string]any             `json:"result"`
	AuditRecorded bool                       `json:"audit_recorded"`
	Resource      *settings.ResourceChange   `json:"resource,omitempty"`
	BeforeRaw     *string                    `json:"before_raw,omitempty"`
	Route         *settings.RouteTestRequest `json:"route,omitempty"`
	RouteStarted  bool                       `json:"route_started,omitempty"`
	Applied       *control.Proposal          `json:"applied,omitempty"`
	Undoes        string                     `json:"undoes,omitempty"`
}

func connectorPath(project string) string {
	return filepath.Join(project, ".agents/eagent/finalechat-connector.json")
}
func randomID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

// Pair connects this account’s own installation. The scoped
// secret stays in a private project file and is excluded from every archive.
func Pair(ctx context.Context, project, name string) (string, error) {
	return pairResource(ctx, project, name, "", connectorPath(project), (&settings.Service{Project: project}).Grant())
}

func pairResource(ctx context.Context, project, name, session, path string, grant control.Grant) (string, error) {
	if finalechat.Disabled() {
		return "", finalechat.ErrDisabled
	}
	var existing connectorConfig
	if err := readConnectorFile(path, &existing); err == nil {
		if existing.ID != "" && existing.Session == session {
			account, err := settingsAccount(project)
			if err != nil {
				return "", err
			}
			if strings.TrimRight(account.BaseURL, "/") != strings.TrimRight(existing.BaseURL, "/") {
				return "", fmt.Errorf("connector belongs to a different FinaleChat server")
			}
			scoped := &finalechat.Client{BaseURL: existing.BaseURL, Token: existing.Secret}
			var status struct {
				Connector connectorInfo `json:"connector"`
			}
			if err = scoped.Request(ctx, "GET", "/api/v1/connectors/"+url.PathEscape(existing.ID), nil, nil, &status, 0); err != nil {
				return "", err
			}
			if status.Connector.State == "pending" {
				err = account.Request(ctx, "POST", "/api/v1/connectors/"+url.PathEscape(existing.ID)+"/connect", nil, map[string]any{"secret": existing.Secret}, nil, 0)
				if err != nil {
					return "", err
				}
			}
			if status.Connector.State == "revoked" {
				return "", fmt.Errorf("this connector was disconnected in FinaleChat")
			}
			return strings.TrimRight(existing.BaseURL, "/") + "/", nil
		}
		return "", fmt.Errorf("repair or remove the invalid local connector file first")
	} else if !os.IsNotExist(err) {
		return "", err
	}
	cfg, err := config.Load(project, "")
	if err != nil {
		return "", err
	}
	client, ok := finalechat.Resolve(cfg.Finalechat.TokenEnvName(), cfg.Finalechat.BaseURL)
	if !ok {
		return "", fmt.Errorf("pairing needs a FinaleChat API token")
	}
	if name == "" {
		host, _ := os.Hostname()
		name = "eagent · " + host + " · " + filepath.Base(project)
	}
	var response struct {
		Connector   connectorInfo `json:"connector"`
		Secret      string        `json:"secret"`
		ApprovalURL string        `json:"approval_url"`
	}
	if err := client.Request(ctx, "POST", "/api/v1/connectors", nil, map[string]any{"name": name, "provider": "eagent", "requested_grants": []control.Grant{grant}, "connect": true}, &response, 0); err != nil {
		return "", err
	}
	if response.Connector.ID == "" || !strings.HasPrefix(response.Secret, "fcc_") {
		return "", fmt.Errorf("invalid pairing response")
	}
	local := connectorConfig{ID: response.Connector.ID, Secret: response.Secret, BaseURL: client.BaseURL, ApprovalURL: response.ApprovalURL, Session: session}
	if err := writeJSONAtomic(path, local); err != nil {
		return "", err
	}
	return strings.TrimRight(client.BaseURL, "/") + "/", nil
}

// startConnectorAt elects one process per explicitly paired resource.
func startConnectorAt(ctx context.Context, project, session, path, lockPath string, logf func(string, ...any)) func() {
	if finalechat.Disabled() {
		return func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	if logf == nil {
		logf = func(string, ...any) {}
	}
	go func() {
		defer close(done)
		for ctx.Err() == nil {
			if _, err := os.Stat(path); err == nil || session == "" {
				lockCtx, stop := context.WithTimeout(ctx, 100*time.Millisecond)
				unlock, err := filelock.Acquire(lockCtx, lockPath)
				stop()
				if err == nil {
					if session == "" {
						connectCtx, stop := context.WithTimeout(ctx, 30*time.Second)
						autoConnectProject(connectCtx, project)
						stop()
					}
					err = runConnectorAt(ctx, project, session, path, logf)
					unlock()
					if err != nil && ctx.Err() == nil {
						logf("settings connector: %v (will retry)", err)
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

func RunConnector(ctx context.Context, project string, logf func(string, ...any)) error {
	return runConnectorAt(ctx, project, "", connectorPath(project), logf)
}

var errConnectorOccupied = errors.New("another settings connection is still active; waiting for it to disconnect")

func runConnectorAt(ctx context.Context, project, session, path string, logf func(string, ...any)) error {
	if finalechat.Disabled() {
		return finalechat.ErrDisabled
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	var local connectorConfig
	if err := readConnectorFile(path, &local); err != nil {
		return err
	}
	if local.ID == "" || !strings.HasPrefix(local.Secret, "fcc_") || local.Session != session {
		return fmt.Errorf("invalid connector configuration")
	}
	client := &finalechat.Client{BaseURL: local.BaseURL, Token: local.Secret, UserAgent: "eagent-settings/1"}
	base := "/api/v1/connectors/" + url.PathEscape(local.ID)
	instance := randomID()
	heartbeatAttempted := false
	// Retain the same identity and local election lock across network retries.
	// Otherwise a dropped response makes us conflict with our own live lease.
	defer func() {
		if !heartbeatAttempted {
			return
		}
		releaseCtx, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		_ = client.Request(releaseCtx, "POST", base+"/release", nil, map[string]any{"instance": instance}, nil, 0)
	}()
	lastError := ""
	for ctx.Err() == nil {
		err := runConnectorConnection(ctx, project, session, local, client, base, instance, &heartbeatAttempted, logf)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, finalechat.ErrDisabled) {
			return err
		}
		var apiErr *finalechat.Error
		if errors.As(err, &apiErr) && !apiErr.Temporary() && apiErr.Status != 409 {
			return err
		}
		if err != nil && err.Error() != lastError {
			lastError = err.Error()
			logf("settings connector: %v (will retry)", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
	return ctx.Err()
}

func runConnectorConnection(ctx context.Context, project, session string, local connectorConfig, client *finalechat.Client, base, instance string, heartbeatAttempted *bool, logf func(string, ...any)) error {
	service := &settings.Service{Project: project}
	expectedGrant := service.Grant()
	if session != "" {
		var err error
		expectedGrant, err = runtimecontrol.Grant(project, session)
		if err != nil {
			return err
		}
	}
	lastVersion := ""
	resourceID := ""
	var linked []settingsThread
	var account *finalechat.Client
	var discovered time.Time
	sitesVersion := map[string]string{}
	for ctx.Err() == nil {
		var status struct {
			Connector connectorInfo `json:"connector"`
		}
		if err := client.Request(ctx, "GET", base, nil, nil, &status, 0); err != nil {
			return err
		}
		if status.Connector.State != "active" {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Second):
				continue
			}
		}
		var grant *control.Grant
		for _, g := range status.Connector.Grants {
			if g.Key == expectedGrant.Key && g.Scope == expectedGrant.Scope {
				copy := g
				grant = &copy
				break
			}
		}
		if grant == nil {
			return fmt.Errorf("this connector has no grant for this project")
		}
		*heartbeatAttempted = true
		if err := client.Request(ctx, "POST", base+"/heartbeat", nil, map[string]any{"instance": instance}, nil, 0); err != nil {
			var apiErr *finalechat.Error
			if errors.As(err, &apiErr) && apiErr.Status == 409 {
				return errConnectorOccupied
			}
			return err
		}
		var view settings.RemoteView
		var err error
		if session == "" {
			view, err = service.RemoteSnapshot(*grant)
		} else {
			view, err = runtimeSnapshot(project, session, *grant)
		}
		if err != nil {
			return err
		}
		if session == "" && time.Since(discovered) > time.Minute {
			discovered = time.Now()
			account, _ = settingsAccount(project)
			if account != nil && strings.TrimRight(account.BaseURL, "/") == strings.TrimRight(local.BaseURL, "/") {
				discoverCtx, stop := context.WithTimeout(ctx, 20*time.Second)
				linked = settingsThreads(discoverCtx, project, account)
				stop()
			} else {
				account = nil
				linked = nil
			}
		}
		if session == "" {
			ids := []string{}
			for _, thread := range linked {
				ids = append(ids, thread.External)
			}
			view.Snapshot.Details["thread_external_ids"] = ids
		}
		var published struct {
			Resource resourceInfo `json:"resource"`
		}
		// Refresh bindings with current artifact revisions even when no settings
		// changed. Snapshot timestamps are kept distinct from heartbeat liveness.
		descriptorRaw, _ := json.Marshal(view.Descriptor)
		publicationVersion := fmt.Sprintf("%s:%t:%s", view.Snapshot.Version, view.Snapshot.RuntimeKnown, artifact.Digest(descriptorRaw))
		hints, _ := json.Marshal(linked)
		publicationVersion += artifact.Digest(hints)
		if publicationVersion != lastVersion {
			if err := client.Request(ctx, "PUT", base+"/resources/"+grant.Key, nil, map[string]any{"instance": instance, "descriptor": view.Descriptor, "snapshot": view.Snapshot, "generation": view.Generation}, &published, 0); err != nil {
				return err
			}
			lastVersion = publicationVersion
			resourceID = published.Resource.ID
		}
		if resourceID != "" && session == "" {
			if account != nil {
				// Bound each publication pass so command delivery stays responsive.
				publishCtx, stop := context.WithTimeout(ctx, 20*time.Second)
				for _, thread := range linked {
					if publishCtx.Err() != nil {
						break
					}
					if sitesVersion[thread.External] == publicationVersion {
						continue
					}
					err := publishSettingsSite(publishCtx, project, "thread-settings-v1", thread, view, account, client, base, resourceID)
					if err == nil {
						sitesVersion[thread.External] = publicationVersion
					}
					if err != nil && ctx.Err() == nil {
						logf("settings website: %v", err)
					}
				}
				stop()
			}
			if err := bindPublished(ctx, client, base, project, resourceID); err != nil && ctx.Err() == nil {
				logf("settings artifact binding: %v", err)
			}
		}
		var claimed struct {
			Command   *command     `json:"command"`
			Resource  resourceInfo `json:"resource"`
			Reconcile bool         `json:"reconcile_only"`
		}
		if err := client.Request(ctx, "POST", base+"/commands/claim", url.Values{"wait": {"25"}}, map[string]any{"instance": instance}, &claimed, 25); err != nil {
			return err
		}
		if claimed.Command == nil {
			continue
		}
		q := *claimed.Command
		if claimed.Resource.Key != grant.Key || q.ResourceID != claimed.Resource.ID {
			return fmt.Errorf("command targeted an unregistered local resource")
		}
		// Renew immediately before entering the writer. No external action runs
		// if the server has already fenced or expired this claim.
		claim := map[string]any{"claim_token": q.ClaimToken, "instance": instance}
		if err := client.Request(ctx, "POST", "/api/v1/commands/"+q.ID+"/renew", nil, claim, nil, 0); err != nil {
			return err
		}
		execCtx, stop := context.WithDeadline(ctx, q.Expires)
		keepDone := make(chan struct{})
		go func() {
			defer close(keepDone)
			ticker := time.NewTicker(20 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-execCtx.Done():
					return
				case <-ticker.C:
					if client.Request(execCtx, "POST", base+"/heartbeat", nil, map[string]any{"instance": instance}, nil, 0) != nil || client.Request(execCtx, "POST", "/api/v1/commands/"+q.ID+"/renew", nil, claim, nil, 0) != nil {
						stop()
						return
					}
				}
			}
		}()
		var journal commandJournal
		var executeErr error
		if session == "" {
			journal, executeErr = executeCommand(execCtx, service, *grant, q, claimed.Reconcile)
		} else {
			journal, executeErr = executeRuntimeCommand(execCtx, project, session, *grant, q, claimed.Reconcile)
		}
		stop()
		<-keepDone
		if executeErr != nil {
			return executeErr
		}
		if err := client.Request(ctx, "POST", "/api/v1/commands/"+q.ID+"/result", nil, map[string]any{"claim_token": q.ClaimToken, "instance": instance, "status": journal.Status, "result": journal.Result}, nil, 0); err != nil {
			return err
		}
		lastVersion = "" // publish the new snapshot after durable acknowledgement
	}
	return ctx.Err()
}

func bindPublished(ctx context.Context, c *finalechat.Client, base, project, resource string) error {
	entries, err := os.ReadDir(stateDir(project))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(stateDir(project), entry.Name(), "publication.json"))
		if err != nil {
			continue
		}
		var pub publication
		if json.Unmarshal(raw, &pub) != nil || !pub.Completed || pub.RevisionID == nil {
			continue
		}
		if err := c.Request(ctx, "PUT", base+"/bindings/"+pub.ArtifactID, nil, map[string]any{"resource_id": resource, "revision_id": *pub.RevisionID}, nil, 0); err != nil {
			return err
		}
	}
	return nil
}

// executeCommand returns the original durable result on duplicate delivery.
// A prepared write is reconciled by exact before/after file hashes, with the
// comparison and any new write under the same lock as the local web editor.
func executeCommand(ctx context.Context, s *settings.Service, grant control.Grant, q command, reconcile bool) (commandJournal, error) {
	if !artifact.ValidPath(q.ID) || strings.Contains(q.ID, "/") || len(q.ID) > 80 {
		return commandJournal{}, fmt.Errorf("invalid command id")
	}
	editor, err := config.LockEditor(ctx, s.Project)
	if err != nil {
		return commandJournal{}, err
	}
	defer editor.Close()
	path := filepath.Join(stateDir(s.Project), "commands", q.ID+".json")
	var journal commandJournal
	if raw, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(raw, &journal); err != nil {
			return journal, err
		}
		if journal.Digest != q.Digest || journal.ResourceKey != grant.Key {
			return journal, fmt.Errorf("command identity changed")
		}
		if journal.Status != "prepared" {
			if !journal.AuditRecorded {
				if err := appendControlAudit(s.Project, q, journal.Status, journal.Result); err != nil {
					return journal, err
				}
				journal.AuditRecorded = true
				if err := writeJSONAtomic(path, journal); err != nil {
					return journal, err
				}
			}
			return journal, nil
		}
	} else if !os.IsNotExist(err) {
		return journal, err
	}
	finish := func(status string, result map[string]any) (commandJournal, error) {
		if status == "succeeded" {
			if offer := undoOffer(journal); offer != nil {
				result["undo"] = offer
			}
			if journal.Undoes != "" {
				result["undoes"] = journal.Undoes
			}
		}
		journal.Status = status
		journal.Result = result
		if err := writeJSONAtomic(path, journal); err != nil {
			return journal, err
		}
		if err := appendControlAudit(s.Project, q, status, result); err != nil {
			return journal, err
		}
		journal.AuditRecorded = true
		return journal, writeJSONAtomic(path, journal)
	}
	if journal.CommandID == "" {
		journal = commandJournal{CommandID: q.ID, Digest: q.Digest, ResourceKey: grant.Key, Status: "prepared"}
		if reconcile && q.Proposal.Operation != "settings.refresh" {
			return finish("unknown", map[string]any{"reason": "local_journal_missing", "message": "A previous claim existed, but its local outcome cannot be established."})
		}
		before, err := s.RemoteSnapshot(grant)
		if err != nil {
			return finish("rejected", map[string]any{"message": err.Error()})
		}
		if q.Proposal.Operation == "settings.refresh" {
			if err := q.Proposal.Validate(before.Descriptor, grant); err != nil {
				return finish("rejected", map[string]any{"message": err.Error()})
			}
			return finish("succeeded", map[string]any{"version": before.Snapshot.Version, "runtime_applied": false, "snapshot_publication": "pending"})
		}
		var prepareErr error
		switch q.Proposal.Operation {
		case "settings.undo":
			prepareErr = prepareUndo(s, editor, grant, q.Proposal, &journal)
		case "route.test":
			route, err := s.PrepareRoute(q.Proposal, grant)
			prepareErr = err
			journal.Route = &route
		case "prompt.set", "prompt.reset", "bundle.save", "bundle.delete":
			change, err := s.PrepareResource(editor, q.Proposal, grant)
			prepareErr = err
			journal.Resource = &change
		default:
			applied := q.Proposal
			journal.Applied = &applied
			desired, etag, _, err := s.Prepare(q.Proposal, grant)
			prepareErr = err
			journal.BeforeETag = etag
			journal.Desired = desired
			journal.DesiredETag = artifact.Digest([]byte(desired))
			raw := config.Resolve(s.Project, s.Preset, s.Bundle).File.Raw
			if raw == "" {
				raw = "{}\n"
			}
			journal.BeforeRaw = &raw
		}
		if prepareErr != nil {
			var conflict *config.ErrConflict
			if errors.As(prepareErr, &conflict) {
				return finish("conflicted", map[string]any{"message": "Settings changed before execution.", "version": before.Snapshot.Version})
			}
			return finish("rejected", map[string]any{"message": prepareErr.Error()})
		}
		if err := writeJSONAtomic(path, journal); err != nil {
			return journal, err
		}
	}
	if err := ctx.Err(); err != nil {
		return journal, err
	}
	if journal.Route != nil {
		if reconcile || journal.RouteStarted {
			return finish("unknown", map[string]any{"message": "The earlier route test has no confirmed result. A potentially paid request was not repeated."})
		}
		if _, err := s.ValidateAction(q.Proposal, grant); err != nil {
			return finish("conflicted", map[string]any{"message": "The route settings changed before the test started."})
		}
		journal.RouteStarted = true
		if err := writeJSONAtomic(path, journal); err != nil {
			return journal, err
		}
		// Freeze the reviewed route, then release the settings writer while the
		// network call runs. A slow probe must not block ordinary config saves.
		editor.Close()
		result, err := s.TestRoute(ctx, *journal.Route)
		if err != nil {
			if ctx.Err() != nil {
				return finish("unknown", map[string]any{"message": "The route test was interrupted. It will not be repeated automatically."})
			}
			return finish("rejected", map[string]any{"message": err.Error()})
		}
		return finish("succeeded", map[string]any{"version": q.Proposal.ExpectedVersion, "tested_settings_version": q.Proposal.ExpectedVersion, "route_test": result, "effects": []any{}, "snapshot_publication": "pending"})
	}
	if journal.Resource != nil {
		before, after, err := journal.Resource.State(editor)
		if err != nil {
			return journal, err
		}
		if !after {
			if !before {
				return finish("conflicted", map[string]any{"message": "The prompt or bundle changed while this command was pending."})
			}
			if reconcile {
				return finish("unknown", map[string]any{"message": "The prepared resource write cannot be confirmed. It was not repeated."})
			}
			if err := journal.Resource.Apply(editor); err != nil {
				return journal, err
			}
		}
		view, err := s.RemoteSnapshot(grant)
		if err != nil {
			return finish("unknown", map[string]any{"message": "Resource saved, but its settings context could not be resolved."})
		}
		return finish("succeeded", map[string]any{"previous_version": q.Proposal.ExpectedVersion, "version": view.Snapshot.Version, "effects": []map[string]any{settings.ResourceEffect(*journal.Resource)}, "snapshot_publication": "pending"})
	}
	res := config.Resolve(s.Project, s.Preset, s.Bundle)
	switch res.File.ETag {
	case journal.DesiredETag: // The save succeeded before a crash or lost ack.
	case journal.BeforeETag:
		if reconcile {
			return finish("unknown", map[string]any{"reason": "prepared_but_unconfirmed", "message": "The prepared write has no confirmed outcome. No edit was repeated."})
		}
		if _, err := editor.SaveRaw(journal.Desired, journal.BeforeETag, true); err != nil {
			return journal, err
		}
	default:
		return finish("conflicted", map[string]any{"message": "The local file changed while this command was pending."})
	}
	after, err := s.RemoteSnapshot(grant)
	if err != nil {
		return finish("unknown", map[string]any{"message": "File saved, but its effective defaults could not be resolved."})
	}
	effects := []map[string]any{}
	applied := q.Proposal
	if journal.Applied != nil {
		applied = *journal.Applied
	}
	for _, edit := range applied.Edits {
		effects = append(effects, map[string]any{"key": edit.Key, "saved": true, "runtime_applied": false, "effective_when": "new_or_resumed_session"})
	}
	return finish("succeeded", map[string]any{"previous_version": q.Proposal.ExpectedVersion, "version": after.Snapshot.Version, "effects": effects, "snapshot_publication": "pending"})
}

func appendControlAudit(project string, q command, status string, result map[string]any) error {
	path := filepath.Join(project, ".agents/eagent/settings-audit.jsonl")
	raw, err := json.Marshal(map[string]any{"format": "finalechat.settings-audit/v1", "id": q.ID + ":" + status, "command_id": q.ID, "user_id": q.UserID, "event": "settings." + status, "proposal_sha256": q.Digest, "proposal": q.Proposal, "result": result, "ts": time.Now().UTC()})
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(raw, '\n')); err != nil {
		return err
	}
	return f.Sync()
}
