package finalechat

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/ericflo/eagent/internal/protocol/artifact"
)

type Artifact struct {
	ID                string  `json:"id"`
	ThreadID          string  `json:"thread_id"`
	CurrentRevisionID *string `json:"current_revision_id"`
}
type ArtifactRevision struct {
	ID       string            `json:"id"`
	Manifest artifact.Manifest `json:"manifest"`
}

func (c *Client) Artifact(ctx context.Context, ref, key, title string) (Artifact, error) {
	var out struct {
		Artifact Artifact `json:"artifact"`
	}
	err := c.Request(ctx, http.MethodPut, "/api/v1/threads/"+refPath(ref)+"/artifacts/"+url.PathEscape(key), nil, map[string]any{"title": title}, &out, 0)
	return out.Artifact, err
}

// CommitArtifact uploads only absent content-addressed chunks, then atomically
// publishes the manifest. Retrying the same key/manifest is safe after a lost
// response; the caller persists the expected parent and key before starting.
func (c *Client) CommitArtifact(ctx context.Context, id string, previous *string, key string, m artifact.Manifest, open func(string) (io.ReadCloser, error)) (ArtifactRevision, error) {
	if Disabled() {
		return ArtifactRevision{}, ErrDisabled
	}
	if err := m.Validate(); err != nil {
		return ArtifactRevision{}, err
	}
	base := "/api/v1/artifacts/" + url.PathEscape(id)
	var check struct {
		Missing []string `json:"missing"`
	}
	hashes := []string{}
	for hash := range m.Blobs() {
		hashes = append(hashes, hash)
	}
	if err := c.Request(ctx, "POST", base+"/blobs/check", nil, map[string]any{"hashes": hashes}, &check, 0); err != nil {
		return ArtifactRevision{}, err
	}
	missing := map[string]bool{}
	for _, hash := range check.Missing {
		missing[hash] = true
	}
	for _, f := range m.Files {
		needed := false
		for _, chunk := range f.Chunks {
			if missing[chunk.SHA256] {
				needed = true
				break
			}
		}
		if !needed {
			continue
		}
		r, err := open(f.Path)
		if err != nil {
			return ArtifactRevision{}, err
		}
		for _, chunk := range f.Chunks {
			if !missing[chunk.SHA256] {
				if _, err = io.CopyN(io.Discard, r, chunk.Size); err != nil {
					break
				}
				continue
			}
			raw := make([]byte, int(chunk.Size))
			if _, err = io.ReadFull(r, raw); err != nil {
				break
			}
			if artifact.Digest(raw) != chunk.SHA256 {
				err = fmt.Errorf("staged artifact file changed: %s", f.Path)
				break
			}
			if Disabled() {
				err = ErrDisabled
				break
			}
			err = c.doRaw(ctx, "PUT", base+"/blobs/"+chunk.SHA256, nil, "application/octet-stream", bytes.NewReader(raw), nil, 0)
			if err != nil {
				break
			}
			delete(missing, chunk.SHA256)
		}
		r.Close()
		if err != nil {
			return ArtifactRevision{}, err
		}
	}
	var out struct {
		Revision ArtifactRevision `json:"revision"`
	}
	err := c.Request(ctx, "POST", base+"/revisions", nil, map[string]any{"manifest": m, "previous_revision_id": previous, "client_key": key}, &out, 0)
	return out.Revision, err
}

// Request exposes the versioned structured connector API to the local
// companion without providing an iframe or model a generic HTTP capability.
func (c *Client) Request(ctx context.Context, method, path string, query url.Values, in, out any, wait int) error {
	if Disabled() {
		return ErrDisabled
	}
	return c.do(ctx, method, path, query, in, out, wait)
}
