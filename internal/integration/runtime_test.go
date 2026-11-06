package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/artifact"
	"github.com/ericflo/eagent/internal/protocol/control"
	"github.com/ericflo/eagent/internal/runtimecontrol"
	"github.com/ericflo/eagent/internal/settings"
	"github.com/ericflo/eagent/internal/store"
)

// fakeConnectorServer is a FinaleChat stand-in that enforces what the real
// server enforces for a connector: one instance holds the heartbeat lease,
// resources publish only under granted keys and by the lease holder, and
// grants change only through /connect with the account token and secret.
type fakeConnectorServer struct {
	t           *testing.T
	mu          sync.Mutex
	grants      []control.Grant
	holder      string
	conflicts   int
	connects    [][]control.Grant
	published   map[string][]control.Snapshot
	queued      *command
	queuedKey   string
	results     chan map[string]any
	publication chan string
}

func newFakeConnectorServer(t *testing.T, grants []control.Grant) (*fakeConnectorServer, *httptest.Server) {
	f := &fakeConnectorServer{t: t, grants: grants, published: map[string][]control.Snapshot{}, results: make(chan map[string]any, 4), publication: make(chan string, 64)}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeConnectorServer) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	instance, _ := body["instance"].(string)
	auth := r.Header.Get("Authorization")
	path := r.URL.Path
	if strings.HasSuffix(path, "/connect") {
		if auth != "Bearer fc_account" || body["secret"] != "fcc_test" {
			w.WriteHeader(403)
			_, _ = w.Write([]byte(`{"error":{"code":"forbidden","message":"forbidden"}}`))
			return
		}
		raw, _ := json.Marshal(body["requested_grants"])
		var grants []control.Grant
		_ = json.Unmarshal(raw, &grants)
		f.mu.Lock()
		f.grants = grants
		f.connects = append(f.connects, grants)
		f.mu.Unlock()
		_, _ = w.Write([]byte(`{}`))
		return
	}
	if auth == "Bearer fc_account" {
		// The optional settings website; not part of this fixture.
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"not found"}}`))
		return
	}
	if auth != "Bearer fcc_test" {
		f.t.Errorf("connector endpoint %s called with %q", path, auth)
		w.WriteHeader(401)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.Method == "GET" && path == "/api/v1/connectors/connector":
		_ = json.NewEncoder(w).Encode(map[string]any{"connector": connectorInfo{ID: "connector", State: "active", Grants: f.grants}})
	case strings.HasSuffix(path, "/heartbeat"):
		if f.holder != "" && f.holder != instance {
			f.conflicts++
			w.WriteHeader(409)
			_, _ = w.Write([]byte(`{"error":{"code":"conflict","message":"another instance holds the lease"}}`))
			return
		}
		f.holder = instance
		_, _ = w.Write([]byte(`{}`))
	case strings.HasSuffix(path, "/release"):
		if f.holder == instance {
			f.holder = ""
		}
		_, _ = w.Write([]byte(`{}`))
	case strings.HasSuffix(path, "/renew"):
		_, _ = w.Write([]byte(`{}`))
	case r.Method == "PUT" && strings.Contains(path, "/resources/"):
		key := path[strings.LastIndex(path, "/")+1:]
		granted := false
		for _, g := range f.grants {
			granted = granted || g.Key == key
		}
		if !granted || instance != f.holder {
			f.t.Errorf("publication of %s: granted=%v instance=%q holder=%q", key, granted, instance, f.holder)
			w.WriteHeader(403)
			return
		}
		raw, _ := json.Marshal(body["snapshot"])
		var snap control.Snapshot
		_ = json.Unmarshal(raw, &snap)
		f.published[key] = append(f.published[key], snap)
		select {
		case f.publication <- key:
		default:
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"resource": resourceInfo{ID: "resource:" + key, Key: key}})
	case strings.HasSuffix(path, "/commands/claim"):
		if q := f.queued; q != nil {
			f.queued = nil
			_ = json.NewEncoder(w).Encode(map[string]any{"command": q, "resource": resourceInfo{ID: q.ResourceID, Key: f.queuedKey}})
			return
		}
		f.mu.Unlock()
		time.Sleep(50 * time.Millisecond)
		f.mu.Lock()
		_, _ = w.Write([]byte(`{}`))
	case strings.HasSuffix(path, "/result"):
		f.results <- body
		_, _ = w.Write([]byte(`{}`))
	default:
		f.t.Errorf("unexpected endpoint: %s %s", r.Method, path)
		w.WriteHeader(404)
	}
}

func (f *fakeConnectorServer) waitFor(t *testing.T, ctx context.Context, what string, ok func() bool) {
	t.Helper()
	for {
		f.mu.Lock()
		done := ok()
		f.mu.Unlock()
		if done {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for %s", what)
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// The project's one connector process publishes a running session's live
// settings under its own lease, prunes grants of sessions that stopped,
// executes the session's commands through the harness, and marks the
// session unavailable when it stops.
func TestProjectConnectorHostsRunningSessionSettings(t *testing.T) {
	t.Setenv("EAGENT_FINALECHAT", "on")
	t.Setenv("FINALECHAT_TOKEN", "fc_account")
	project := t.TempDir()
	sess, err := store.Create(store.Root(project), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
	var applied atomic.Int32
	owner, err := runtimecontrol.New(project, sess.ID, runtimecontrol.FromConfig(config.Defaults()), func(_ runtimecontrol.Request, _ runtimecontrol.Values, _ map[string]any) error {
		applied.Add(1)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var ownerMu sync.Mutex
	ownerOpen := true
	ownerDone := make(chan struct{})
	go func() {
		defer close(ownerDone)
		for ctx.Err() == nil {
			ownerMu.Lock()
			if ownerOpen {
				if err := owner.Poll(); err != nil {
					t.Error(err)
				}
			}
			ownerMu.Unlock()
			time.Sleep(10 * time.Millisecond)
		}
	}()
	projectGrant := (&settings.Service{Project: project}).Grant()
	liveGrant, _ := runtimecontrol.Grant(project, sess.ID)
	staleA, _ := runtimecontrol.Grant(project, "1700000000000")
	staleB, _ := runtimecontrol.Grant(project, "1700000000001")
	f, srv := newFakeConnectorServer(t, []control.Grant{projectGrant, staleA, staleB})
	if err := writeJSONAtomic(config.File(project), map[string]any{"finalechat": map[string]any{"base_url": srv.URL}}); err != nil {
		t.Fatal(err)
	}
	if err := writeJSONAtomic(connectorPath(project), connectorConfig{ID: "connector", Secret: "fcc_test", BaseURL: srv.URL}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- runConnectorAt(ctx, project, connectorPath(project), t.Logf) }()

	// Stale session grants are dropped and the running session's is added.
	f.waitFor(t, ctx, "the grant sync", func() bool { return len(f.connects) > 0 })
	f.mu.Lock()
	first := f.connects[0]
	f.mu.Unlock()
	if len(first) != 2 || first[0].Key != projectGrant.Key || first[1].Key != liveGrant.Key || !slicesContain(first[1].Classes, "route") {
		t.Fatalf("requested grants = %+v", first)
	}
	f.waitFor(t, ctx, "the session resource", func() bool { return len(f.published[liveGrant.Key]) > 0 })
	f.mu.Lock()
	snap := f.published[liveGrant.Key][0]
	f.mu.Unlock()
	if !snap.RuntimeKnown || snap.Details["thread_external_id"] != "eagent:"+sess.ID {
		t.Fatalf("session snapshot = %+v", snap)
	}

	// A command for the session goes to the harness, not the project file.
	view, err := runtimeSnapshot(project, sess.ID, liveGrant)
	if err != nil {
		t.Fatal(err)
	}
	p := control.Proposal{Operation: "settings.apply", SchemaVersion: runtimecontrol.Schema, Generation: view.Generation, ExpectedVersion: view.Snapshot.Version, Edits: []control.Edit{{Op: "set", Key: "/task_concurrency", Value: float64(7)}}}
	raw, _ := json.Marshal(p)
	q := command{ID: randomID(), UserID: "owner", ResourceID: "resource:" + liveGrant.Key, Proposal: p, Digest: artifact.Digest(raw), ClaimToken: "claim", Expires: time.Now().Add(time.Minute)}
	f.mu.Lock()
	f.queued, f.queuedKey = &q, liveGrant.Key
	f.mu.Unlock()
	select {
	case result := <-f.results:
		if result["status"] != "succeeded" || applied.Load() != 1 {
			t.Fatalf("not acknowledged by the runtime: %+v", result)
		}
	case <-ctx.Done():
		t.Fatal("command timed out")
	}
	f.waitFor(t, ctx, "the updated session snapshot", func() bool {
		all := f.published[liveGrant.Key]
		return all[len(all)-1].Saved["/task_concurrency"] == float64(7)
	})
	if _, err := os.Stat(config.File(project)); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(config.File(project)); strings.Contains(string(raw), "task_concurrency") {
		t.Fatal("a session command wrote project defaults")
	}

	// The session stops: it is published as unavailable, then its grant goes.
	ownerMu.Lock()
	ownerOpen = false
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	ownerMu.Unlock()
	f.waitFor(t, ctx, "the stopped session's grant to drop", func() bool {
		last := f.connects[len(f.connects)-1]
		return len(last) == 1 && last[0].Key == projectGrant.Key
	})
	f.mu.Lock()
	all := f.published[liveGrant.Key]
	conflicts := f.conflicts
	f.mu.Unlock()
	if all[len(all)-1].RuntimeKnown {
		t.Fatal("the stopped session was never published as unavailable")
	}
	if conflicts != 0 {
		t.Fatalf("%d heartbeat conflicts: sessions must share the project connector's lease", conflicts)
	}
	cancel()
	<-done
	<-ownerDone

	// A crash after the runtime acknowledged, before the result was sent:
	// reconciliation reads the original outcome and never applies twice.
	journalPath := filepath.Join(stateDir(project), "runtime-commands", q.ID+".json")
	if err := writeJSONAtomic(journalPath, commandJournal{CommandID: q.ID, Digest: q.Digest, ResourceKey: liveGrant.Key, Status: "prepared"}); err != nil {
		t.Fatal(err)
	}
	recovered, err := executeRuntimeCommand(context.Background(), project, sess.ID, liveGrant, q, true)
	if err != nil || recovered.Status != "succeeded" || applied.Load() != 1 {
		t.Fatalf("reconciliation: %+v %v", recovered, err)
	}
	q.ID = randomID()
	unknown, err := executeRuntimeCommand(context.Background(), project, sess.ID, liveGrant, q, true)
	if err != nil || unknown.Status != "unknown" || applied.Load() != 1 {
		t.Fatal("unconfirmed command was repeated", unknown, err)
	}
}

func slicesContain(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

func TestSessionGrantsAreBoundedAndKeepUnrelatedGrants(t *testing.T) {
	project := t.TempDir()
	projectGrant := (&settings.Service{Project: project}).Grant()
	other := control.Grant{Key: "something-else", Label: "x", Scope: "project", Operations: []string{"settings.apply"}, Classes: []string{"preference"}}
	stale, _ := runtimecontrol.Grant(project, "1")
	live := []string{}
	for i := 0; i < 40; i++ {
		live = append(live, fmt.Sprint(1800000000000+i))
	}
	got := sessionGrants(project, []control.Grant{projectGrant, other, stale}, projectGrant, live)
	if len(got) != 2+maxSessionResources-1 || got[0].Key != projectGrant.Key || got[1].Key != other.Key {
		t.Fatalf("grants = %d %+v", len(got), got[:2])
	}
	for _, g := range got {
		if g.Key == stale.Key {
			t.Fatal("a stopped session's grant was kept")
		}
	}
	if !sameGrantKeys(got, append([]control.Grant{}, got...)) || sameGrantKeys(got, got[:len(got)-1]) {
		t.Fatal("sameGrantKeys")
	}
}

func TestRunningSessionsDiscoversLiveHeartbeatOnly(t *testing.T) {
	project := t.TempDir()
	// A session with no runtime status is not "running".
	dead, err := store.Create(store.Root(project), time.UnixMilli(1788800000000))
	if err != nil {
		t.Fatal(err)
	}
	if err := dead.Close(); err != nil {
		t.Fatal(err)
	}
	if got := runningSessions(project); len(got) != 0 {
		t.Fatalf("runningSessions = %v, want none", got)
	}
	// A session whose harness started the runtime control heartbeats.
	live, err := store.Create(store.Root(project), time.UnixMilli(1788800000001))
	if err != nil {
		t.Fatal(err)
	}
	if err := live.Close(); err != nil {
		t.Fatal(err)
	}
	owner, err := runtimecontrol.New(project, live.ID, runtimecontrol.FromConfig(config.Defaults()), func(_ runtimecontrol.Request, _ runtimecontrol.Values, _ map[string]any) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	got := runningSessions(project)
	if len(got) != 1 || got[0] != live.ID {
		t.Fatalf("runningSessions = %v, want [%s]", got, live.ID)
	}
	// When the harness stops, the heartbeat lapses and the session drops out.
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	// Close heartbeats with AvailableUntil zeroed; ReadStatus still returns it,
	// but AvailableUntil.After(now) is false.
	if got := runningSessions(project); len(got) != 0 {
		t.Fatalf("after shutdown runningSessions = %v, want none", got)
	}
}
