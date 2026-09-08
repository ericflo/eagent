package finalechat

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/protocol/artifact"
)

func killSwitchManifest() artifact.Manifest {
	hash := artifact.Digest([]byte("fixture"))
	return artifact.Manifest{Format: artifact.Format, Producer: artifact.Producer{Name: "test"}, CapturedAt: time.Now(), Dataset: map[string]any{}, Entrypoint: "index.html", Files: []artifact.File{{Path: "index.html", Role: "viewer", ContentType: "text/html", Size: 7, SHA256: hash, Chunks: []artifact.Chunk{{SHA256: hash, Size: 7}}}}}
}

func TestArtifactAndConnectorRequestsHonorKillSwitch(t *testing.T) {
	t.Setenv("EAGENT_FINALECHAT", "off")
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer server.Close()
	c := &Client{BaseURL: server.URL, Token: "fc_fixture"}
	ctx := context.Background()
	if _, err := c.Artifact(ctx, "ext:fixture", "session", "fixture"); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
	if err := c.Request(ctx, "POST", "/api/v1/connectors", nil, nil, nil, 0); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
	if _, err := c.CommitArtifact(ctx, "fixture", nil, "key", killSwitchManifest(), func(string) (io.ReadCloser, error) {
		t.Fatal("disabled publication opened a source file")
		return nil, nil
	}); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("kill switch allowed HTTP traffic")
	}
}

func TestKillSwitchIsRecheckedBeforeArtifactUpload(t *testing.T) {
	t.Setenv("EAGENT_FINALECHAT", "on")
	manifest := killSwitchManifest()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"missing": []string{manifest.Files[0].SHA256}})
	}))
	defer server.Close()
	c := &Client{BaseURL: server.URL, Token: "fc_fixture"}
	_, err := c.CommitArtifact(context.Background(), "fixture", nil, "key", manifest, func(string) (io.ReadCloser, error) {
		t.Setenv("EAGENT_FINALECHAT", "off")
		return io.NopCloser(strings.NewReader("fixture")), nil
	})
	if !errors.Is(err, ErrDisabled) || calls.Load() != 1 {
		t.Fatalf("kill switch did not stop upload after blob check: requests=%d err=%v", calls.Load(), err)
	}
}
