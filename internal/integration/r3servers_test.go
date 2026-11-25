package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/artifact"
	"github.com/ericflo/eagent/internal/protocol/control"
	"github.com/ericflo/eagent/internal/settings"
)

// A server list edit that arrives over the connector cannot make eagent send
// a local key variable's value to an address the edit chose.
func TestConnectorServerEditNeverSendsACloudKeyToANewAddress(t *testing.T) {
	const secret = "sk-canary-connector"
	t.Setenv("OPENAI_API_KEY", secret)
	var leaks atomic.Int32
	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, vs := range r.Header {
			for _, v := range vs {
				if strings.Contains(v, secret) {
					leaks.Add(1)
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/models" {
			_, _ = w.Write([]byte(`{"data":[{"id":"bait"}]}`))
			return
		}
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer attacker.Close()

	s := &settings.Service{Project: t.TempDir()}
	g := s.Grant()
	for _, skip := range []bool{false, true} {
		v, err := s.RemoteSnapshot(g)
		if err != nil {
			t.Fatal(err)
		}
		p := control.Proposal{Operation: "settings.apply", SchemaVersion: settings.SchemaVersion, ExpectedVersion: v.Snapshot.Version, Edits: []control.Edit{{Op: "set", Key: "/servers", Value: []any{map[string]any{"url": attacker.URL, "key_env": "OPENAI_API_KEY", "skip_probe": skip}}}}}
		raw, _ := json.Marshal(p)
		q := command{ID: randomID(), UserID: "u1", Proposal: p, Digest: artifact.Digest(raw), Expires: time.Now().Add(time.Minute)}
		res, _ := executeCommand(context.Background(), s, g, q, false)
		if res.Status == "succeeded" {
			t.Errorf("skip_probe=%v: the edit succeeded", skip)
		}
	}
	_, _, _ = config.RefreshAllServers(context.Background(), s.Project)
	if n := leaks.Load(); n != 0 {
		t.Fatalf("the fake server received the key %d times", n)
	}
}
