package integration

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"encoding/json"
	"github.com/ericflo/eagent/internal/archive"
	"github.com/ericflo/eagent/internal/boundedfile"
	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/finalechat"
	"github.com/ericflo/eagent/internal/settings"
	"github.com/ericflo/eagent/internal/store"
)

type settingsThread struct{ ID, Session, External string }

func settingsAccount(project string) (*finalechat.Client, error) {
	if finalechat.Disabled() {
		return nil, finalechat.ErrDisabled
	}
	cfg, err := config.Load(project, "")
	if err != nil {
		return nil, err
	}
	client, ok := finalechat.Resolve(cfg.Finalechat.TokenEnvName(), cfg.Finalechat.BaseURL)
	if !ok {
		return nil, fmt.Errorf("FinaleChat is not connected")
	}
	return client, nil
}

// Only existing conversations for actual local sessions are considered. A
// settings connection never creates empty conversations or uploads their logs.
func settingsThreads(ctx context.Context, project string, client *finalechat.Client) []settingsThread {
	infos, err := store.List(store.Root(project))
	if err != nil {
		return nil
	}
	if len(infos) > 100 {
		infos = infos[len(infos)-100:]
	}
	var out []settingsThread
	for _, info := range infos {
		if ctx.Err() != nil {
			break
		}
		external := "eagent:" + info.ID
		var response struct {
			Thread struct {
				ID string `json:"id"`
			}
		}
		if client.Request(ctx, "GET", "/api/v1/threads/"+url.PathEscape("ext:"+external), nil, nil, &response, 0) == nil && response.Thread.ID != "" {
			out = append(out, settingsThread{response.Thread.ID, info.ID, external})
		}
	}
	return out
}

func autoConnectProject(ctx context.Context, project string) {
	if finalechat.Disabled() {
		return
	}
	if _, err := os.Stat(connectorPath(project)); err == nil {
		_, _ = Pair(ctx, project, "")
		return
	}
	infos, _ := store.List(store.Root(project))
	if len(infos) == 0 {
		return
	}
	client, err := settingsAccount(project)
	if err != nil {
		return
	}
	if len(settingsThreads(ctx, project, client)) > 0 {
		_, _ = Pair(ctx, project, "")
	}
}

// The existing artifact store keeps this small website and its history. Its
// publication is independent of the opt-out for native transcript backups.
func publishSettingsSite(ctx context.Context, project, version string, thread settingsThread, view settings.RemoteView, account, connector *finalechat.Client, base, resourceID string) error {
	site, err := archive.SettingsWebsite(project, thread.Session, version, view)
	if err != nil {
		return err
	}
	defer site.Close()
	return publishSite(ctx, project, thread.Session, site, func(ctx context.Context) (finalechat.Artifact, error) {
		return account.Artifact(ctx, "ext:"+thread.External, "agent-settings", "Eagent settings")
	}, account, connector, base, resourceID)
}

// publishResourceSite publishes the project's settings editor for the
// resource itself, the page a new-session draft opens before any
// conversation exists. It carries no session context.
func publishResourceSite(ctx context.Context, project, version string, view settings.RemoteView, account, connector *finalechat.Client, base, resourceID string) error {
	site, err := archive.SettingsWebsite(project, "", version, view)
	if err != nil {
		return err
	}
	defer site.Close()
	return publishSite(ctx, project, "project", site, func(ctx context.Context) (finalechat.Artifact, error) {
		return account.ResourceArtifact(ctx, resourceID, "Eagent settings")
	}, account, connector, base, resourceID)
}

// publishSite records a website's artifact, revision and fingerprint under
// the given name so retries and restarts reuse them, commits a new revision
// only when the content changed, and binds the current revision to the
// resource with the connector's credential.
func publishSite(ctx context.Context, project, name string, site *archive.Export, register func(context.Context) (finalechat.Artifact, error), account, connector *finalechat.Client, base, resourceID string) error {
	path := filepath.Join(stateDir(project), "settings-sites", name+".json")
	var state struct {
		ArtifactID  string    `json:"artifact_id"`
		RevisionID  *string   `json:"revision_id"`
		Fingerprint string    `json:"fingerprint"`
		Deleted     bool      `json:"deleted"`
		PendingKey  string    `json:"pending_key"`
		CapturedAt  time.Time `json:"captured_at"`
	}
	if raw, err := boundedfile.Read(path, 96<<10); err == nil {
		if err = json.Unmarshal(raw, &state); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if state.Deleted {
		return nil
	}
	if state.ArtifactID == "" {
		a, err := register(ctx)
		if err != nil {
			return err
		}
		state.ArtifactID = a.ID
		if err = writeJSONAtomic(path, state); err != nil {
			return err
		}
	}
	head, err := account.ArtifactHead(ctx, state.ArtifactID)
	if err != nil {
		var apiErr *finalechat.Error
		if errors.As(err, &apiErr) && apiErr.Status == 404 {
			state.Deleted = true
			_ = writeJSONAtomic(path, state)
		}
		return err
	}
	fp := fingerprint(site.Manifest)
	if head.Revision == nil || fingerprint(head.Revision.Manifest) != fp {
		// Persist the capture time before upload so retries use identical bytes.
		key := "settings-" + fp
		if head.Artifact.CurrentRevisionID != nil {
			key += "-" + *head.Artifact.CurrentRevisionID
		}
		if state.PendingKey != key {
			state.PendingKey = key
			state.CapturedAt = site.Manifest.CapturedAt
			if err = writeJSONAtomic(path, state); err != nil {
				return err
			}
		}
		site.Manifest.CapturedAt = state.CapturedAt
		revision, err := account.CommitArtifact(ctx, state.ArtifactID, head.Artifact.CurrentRevisionID, key, site.Manifest, func(name string) (io.ReadCloser, error) {
			return os.Open(filepath.Join(site.Dir, filepath.FromSlash(name)))
		})
		if err != nil {
			return err
		}
		state.RevisionID = &revision.ID
	} else {
		state.RevisionID = &head.Revision.ID
	}
	state.Fingerprint = fp
	if err = writeJSONAtomic(path, state); err != nil {
		return err
	}
	return connector.Request(ctx, "PUT", base+"/bindings/"+state.ArtifactID, nil, map[string]any{"resource_id": resourceID, "revision_id": state.RevisionID}, nil, 0)
}
