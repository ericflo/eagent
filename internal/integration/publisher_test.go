package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/archive"
	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/finalechat"
	"github.com/ericflo/eagent/internal/protocol/artifact"
	"github.com/ericflo/eagent/internal/store"
)

type publicationFixture struct {
	legacy                        bool
	mu                            sync.Mutex
	head                          finalechat.ArtifactHead
	blobs                         map[string][]byte
	keys                          map[string]finalechat.ArtifactRevision
	attempts                      []string
	registrations, commits, calls int
	failBefore, failAfter         bool
	resourceRegistrations         int
}

func (f *publicationFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	w.Header().Set("Content-Type", "application/json")
	send := func(value any) { _ = json.NewEncoder(w).Encode(value) }
	if r.URL.Path == "/api/v1/me" {
		features := []string{"artifacts.v1"}
		if f.legacy {
			features = nil
		}
		send(map[string]any{"features": features})
		return
	}
	if r.Header.Get("Authorization") != "Bearer fc_fixture" {
		w.WriteHeader(401)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/v1/settings-resources/") && strings.HasSuffix(r.URL.Path, "/website") && r.Method == "PUT" {
		f.resourceRegistrations++
		if f.head.Artifact.ID == "" {
			f.head = finalechat.ArtifactHead{Artifact: finalechat.Artifact{ID: fmt.Sprintf("resource-artifact-%d", f.resourceRegistrations)}}
		}
		send(map[string]any{"artifact": f.head.Artifact})
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/v1/threads/") && r.Method == "PUT" {
		f.registrations++
		if f.head.Artifact.ID == "" {
			f.head = finalechat.ArtifactHead{Artifact: finalechat.Artifact{ID: fmt.Sprintf("artifact-%d", f.registrations), ThreadID: "fixture-thread"}}
		}
		send(map[string]any{"artifact": f.head.Artifact})
		return
	}
	base := "/api/v1/artifacts/" + f.head.Artifact.ID
	if f.head.Artifact.ID == "" || !strings.HasPrefix(r.URL.Path, base) {
		w.WriteHeader(404)
		return
	}
	if r.Method == "GET" && r.URL.Path == base {
		send(f.head)
		return
	}
	if r.URL.Path == base+"/blobs/check" {
		var in struct {
			Hashes []string `json:"hashes"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		missing := []string{}
		for _, hash := range in.Hashes {
			if _, ok := f.blobs[hash]; !ok {
				missing = append(missing, hash)
			}
		}
		send(map[string]any{"missing": missing})
		return
	}
	if strings.HasPrefix(r.URL.Path, base+"/blobs/") && r.Method == "PUT" {
		raw, _ := io.ReadAll(io.LimitReader(r.Body, artifact.MaxBlobBytes+1))
		hash := strings.TrimPrefix(r.URL.Path, base+"/blobs/")
		if artifact.Digest(raw) != hash {
			w.WriteHeader(422)
			return
		}
		f.blobs[hash] = raw
		send(map[string]any{})
		return
	}
	if r.URL.Path == base+"/revisions" && r.Method == "POST" {
		var in struct {
			Manifest artifact.Manifest `json:"manifest"`
			Parent   *string           `json:"previous_revision_id"`
			Key      string            `json:"client_key"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil || in.Manifest.Validate() != nil {
			w.WriteHeader(422)
			return
		}
		f.attempts = append(f.attempts, in.Key)
		if prior, ok := f.keys[in.Key]; ok {
			send(map[string]any{"revision": prior})
			return
		}
		if f.failBefore {
			f.failBefore = false
			w.WriteHeader(503)
			return
		}
		if !sameRevision(f.head.Artifact.CurrentRevisionID, in.Parent) {
			w.WriteHeader(409)
			return
		}
		for hash := range in.Manifest.Blobs() {
			if _, ok := f.blobs[hash]; !ok {
				w.WriteHeader(422)
				return
			}
		}
		f.commits++
		rev := finalechat.ArtifactRevision{ID: fmt.Sprintf("revision-%d", f.commits), Manifest: in.Manifest}
		f.head.Revision = &rev
		f.head.Artifact.CurrentRevisionID = &rev.ID
		f.keys[in.Key] = rev
		if f.failAfter {
			f.failAfter = false
			w.WriteHeader(503)
			return
		}
		send(map[string]any{"revision": rev})
		return
	}
	w.WriteHeader(404)
}

func newPublicationFixture(t *testing.T) (*publicationFixture, string, *store.Session) {
	t.Helper()
	f := &publicationFixture{blobs: map[string][]byte{}, keys: map[string]finalechat.ArtifactRevision{}}
	server := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(server.Close)
	t.Setenv("EAGENT_FINALECHAT", "")
	t.Setenv("FINALECHAT_TOKEN", "fc_fixture")
	project := t.TempDir()
	if err := writeJSONAtomic(config.File(project), map[string]any{"finalechat": map[string]any{"artifacts": true, "enabled": false, "base_url": server.URL}}); err != nil {
		t.Fatal(err)
	}
	session, err := store.Create(store.Root(project), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	if _, err := session.Append(event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{})); err != nil {
		t.Fatal(err)
	}
	return f, project, session
}

func appendPublicationEvent(t *testing.T, session *store.Session, text string) {
	t.Helper()
	if _, err := session.Append(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: text})); err != nil {
		t.Fatal(err)
	}
}

func publicationRecord(t *testing.T, project, id string) publication {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(stateDir(project), id, "publication.json"))
	if err != nil {
		t.Fatal(err)
	}
	var record publication
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatal(err)
	}
	return record
}

func TestSourceSignatureTracksRecoveryOfUnavailableSettings(t *testing.T) {
	project := t.TempDir()
	session, err := store.Create(store.Root(project), time.UnixMilli(1788800000000))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Append(event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{})); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := store.Resolve(store.Root(project), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	path := config.File(project)
	if err := os.WriteFile(path, []byte(`{"broken":`), 0600); err != nil {
		t.Fatal(err)
	}
	unavailable, err := sourceSignature(project, info, "test")
	if err != nil {
		t.Fatal(err)
	}
	again, err := sourceSignature(project, info, "test")
	if err != nil || again != unavailable {
		t.Fatalf("unstable unavailable capture: %v", err)
	}
	if err := os.WriteFile(path, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	repaired, err := sourceSignature(project, info, "test")
	if err != nil || repaired == unavailable {
		t.Fatalf("repair not detected: %v", err)
	}
}

func TestPublisherReconcilesLostAcknowledgementBeforeNewCapture(t *testing.T) {
	f, project, session := newPublicationFixture(t)
	f.failAfter = true
	if _, err := Publish(context.Background(), project, session.ID, "test", true); err == nil {
		t.Fatal("missing acknowledgement reported as success")
	}
	pending := publicationRecord(t, project, session.ID)
	if pending.Completed || pending.Directory == "" {
		t.Fatal("lost immutable intent")
	}
	appendPublicationEvent(t, session, "written after the lost acknowledgement")
	if _, err := Publish(context.Background(), project, session.ID, "test", true); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.commits != 2 || len(f.attempts) != 3 || f.attempts[0] != f.attempts[1] || f.attempts[1] == f.attempts[2] {
		t.Fatalf("lost acknowledgement was not replayed idempotently: commits=%d attempts=%v", f.commits, f.attempts)
	}
	if _, err := os.Stat(pending.Directory); !os.IsNotExist(err) {
		t.Fatal("completed staging data was not removed")
	}
	if !publicationRecord(t, project, session.ID).Completed {
		t.Fatal("completed result not durable")
	}
}

func TestPublisherFeatureDetectsOlderDeployment(t *testing.T) {
	f, project, session := newPublicationFixture(t)
	f.legacy = true
	if _, err := Publish(context.Background(), project, session.ID, "test", true); !errors.Is(err, ErrArtifactsUnsupported) {
		t.Fatalf("unsupported deployment: %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.registrations != 0 || f.commits != 0 {
		t.Fatal("unsupported deployment received artifact mutations")
	}
}

func TestPublisherRecapturesAfterCompetingParentAndPreservesBothHistories(t *testing.T) {
	f, project, session := newPublicationFixture(t)
	if _, err := Publish(context.Background(), project, session.ID, "test", true); err != nil {
		t.Fatal(err)
	}
	appendPublicationEvent(t, session, "pending native event")
	f.mu.Lock()
	f.failBefore = true
	f.mu.Unlock()
	if _, err := Publish(context.Background(), project, session.ID, "test", true); err == nil {
		t.Fatal("expected upload interruption")
	}
	pending := publicationRecord(t, project, session.ID)
	appendPublicationEvent(t, session, "event published by the competing writer")
	remote, err := archive.Snapshot(context.Background(), project, session.ID, "other-writer")
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()
	f.mu.Lock()
	rev := finalechat.ArtifactRevision{ID: "competing-revision", Manifest: remote.Manifest}
	f.head.Revision = &rev
	f.head.Artifact.CurrentRevisionID = &rev.ID
	f.mu.Unlock()
	logs, _ := filepath.Glob(filepath.Join(session.Path, "*.jsonl"))
	raw, err := os.ReadFile(logs[0])
	if err != nil {
		t.Fatal(err)
	}
	previous, err := os.ReadFile(filepath.Join(pending.Directory, "sessions", filepath.Base(logs[0])))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logs[0], previous, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Publish(context.Background(), project, session.ID, "test", true); !errors.Is(err, ErrPublicationConflict) {
		t.Fatalf("truncated source advanced current: %v", err)
	}
	retained := publicationRecord(t, project, session.ID)
	if !retained.Recapture || retained.ClientKey != pending.ClientKey {
		t.Fatal("conflict lost original capture identity")
	}
	if _, err := os.Stat(pending.Directory); err != nil {
		t.Fatal("conflict discarded immutable pending capture", err)
	}
	if err := os.WriteFile(logs[0], raw, 0600); err != nil {
		t.Fatal(err)
	}
	appendPublicationEvent(t, session, "new capture extends both histories")
	if _, err := Publish(context.Background(), project, session.ID, "test", true); err != nil {
		t.Fatal(err)
	}
	completed := publicationRecord(t, project, session.ID)
	if !completed.Completed || completed.Recapture || completed.ClientKey == pending.ClientKey {
		t.Fatal("fresh capture reused the stale manifest identity")
	}
	if _, err := os.Stat(pending.Directory); !os.IsNotExist(err) {
		t.Fatal("superseded capture was not cleaned after durable replacement")
	}
}

func TestPublisherDeletionPausesEvenUnchangedAndRequiresExplicitRecreation(t *testing.T) {
	f, project, session := newPublicationFixture(t)
	id, err := Publish(context.Background(), project, session.ID, "test", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RecreatePublication(context.Background(), project, session.ID, "test"); err == nil {
		t.Fatal("recreated a still-existing archive")
	}
	f.mu.Lock()
	f.head = finalechat.ArtifactHead{}
	f.mu.Unlock()
	if _, err := Publish(context.Background(), project, session.ID, "test", true); !errors.Is(err, ErrPublicationDeleted) {
		t.Fatalf("unchanged deleted artifact was not detected: %v", err)
	}
	if !publicationRecord(t, project, session.ID).Deleted {
		t.Fatal("deletion tombstone is not durable")
	}
	f.mu.Lock()
	calls := f.calls
	f.mu.Unlock()
	appendPublicationEvent(t, session, "later event must not resurrect deletion")
	if _, err := Publish(context.Background(), project, session.ID, "test", true); !errors.Is(err, ErrPublicationDeleted) {
		t.Fatalf("forced publish bypassed deletion: %v", err)
	}
	f.mu.Lock()
	afterCalls := f.calls
	f.mu.Unlock()
	if afterCalls != calls {
		t.Fatal("paused publisher continued making HTTP requests")
	}
	newID, err := RecreatePublication(context.Background(), project, session.ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	if id == newID || newID == "" {
		t.Fatal("recreation did not create a distinct record")
	}
	retired, _ := filepath.Glob(filepath.Join(stateDir(project), session.ID, "retired-*.json"))
	if len(retired) != 1 {
		t.Fatal("recreation discarded historical identity")
	}
}

func TestPublisherRefusesDivergentSourcesAndUnconfinedPendingDirectory(t *testing.T) {
	f, project, session := newPublicationFixture(t)
	appendPublicationEvent(t, session, "original message")
	if _, err := Publish(context.Background(), project, session.ID, "test", true); err != nil {
		t.Fatal(err)
	}
	logs, _ := filepath.Glob(filepath.Join(session.Path, "*.jsonl"))
	raw, _ := os.ReadFile(logs[0])
	if err := os.WriteFile(logs[0], bytes.Replace(raw, []byte("original message"), []byte("modified message"), 1), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Publish(context.Background(), project, session.ID, "test", true); !errors.Is(err, ErrPublicationConflict) {
		t.Fatalf("divergent source was published: %v", err)
	}
	f.mu.Lock()
	commits := f.commits
	f.mu.Unlock()
	if commits != 1 {
		t.Fatal("conflict changed the published record")
	}
	state := publicationRecord(t, project, session.ID)
	state.Completed = false
	state.Directory = t.TempDir()
	if err := writeJSONAtomic(filepath.Join(stateDir(project), session.ID, "publication.json"), state); err != nil {
		t.Fatal(err)
	}
	if _, err := Publish(context.Background(), project, session.ID, "test", true); err == nil || !strings.Contains(err.Error(), "outside its private staging") {
		t.Fatalf("unconfined staging path accepted: %v", err)
	}
}
