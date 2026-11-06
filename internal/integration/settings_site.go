package integration

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"time"

	"encoding/json"
	"github.com/ericflo/eagent/internal/archive"
	"github.com/ericflo/eagent/internal/boundedfile"
	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/finalechat"
	"github.com/ericflo/eagent/internal/settings"
	"github.com/ericflo/eagent/internal/store"
)

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

// hasProjectThread reports whether any of this project's newest sessions
// already has a FinaleChat conversation. Connecting settings never creates
// conversations or uploads logs; it only follows ones that exist.
func hasProjectThread(ctx context.Context, project string, client *finalechat.Client) bool {
	infos, err := store.List(store.Root(project))
	if err != nil {
		return false
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].Started.After(infos[j].Started) })
	if len(infos) > 20 {
		infos = infos[:20]
	}
	for _, info := range infos {
		if ctx.Err() != nil {
			return false
		}
		var response struct {
			Thread struct {
				ID string `json:"id"`
			}
		}
		if client.Request(ctx, "GET", "/api/v1/threads/"+url.PathEscape("ext:eagent:"+info.ID), nil, nil, &response, 0) == nil && response.Thread.ID != "" {
			return true
		}
	}
	return false
}

func autoConnectProject(ctx context.Context, project string) {
	if finalechat.Disabled() {
		return
	}
	if _, err := os.Stat(connectorPath(project)); err == nil {
		_, _ = Pair(ctx, project, "")
		return
	}
	client, err := settingsAccount(project)
	if err != nil {
		return
	}
	if hasProjectThread(ctx, project, client) {
		_, _ = Pair(ctx, project, "")
	}
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
		if finalechat.Gone(err) {
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
