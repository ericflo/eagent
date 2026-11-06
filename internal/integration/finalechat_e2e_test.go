package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/runtimecontrol"
	"github.com/ericflo/eagent/internal/store"
)

// TestFinaleChatEndToEnd drives the real connector against a real FinaleChat
// server (a local one: open sign-up, memory blob store), the path the phone
// takes: a running session's own settings appear in its thread ahead of the
// project defaults, a Save there reaches the running harness, and when the
// session stops its settings leave the thread. Fakes cannot catch what the
// server enforces (one heartbeat lease per connector, grant checks), so this
// runs against the real thing when EAGENT_E2E_FINALECHAT_URL is set:
//
//	EAGENT_E2E_FINALECHAT_URL=http://127.0.0.1:18080 go test ./internal/integration -run FinaleChatEndToEnd
func TestFinaleChatEndToEnd(t *testing.T) {
	base := strings.TrimRight(os.Getenv("EAGENT_E2E_FINALECHAT_URL"), "/")
	if base == "" {
		t.Skip("set EAGENT_E2E_FINALECHAT_URL to a local FinaleChat server with open sign-up")
	}
	t.Setenv("EAGENT_FINALECHAT", "on")
	jar, _ := cookiejar.New(nil)
	browser := &http.Client{Jar: jar, Timeout: 30 * time.Second}
	call := func(token, method, path string, body any, out any) int {
		t.Helper()
		var reader *bytes.Reader
		if body != nil {
			raw, _ := json.Marshal(body)
			reader = bytes.NewReader(raw)
		} else {
			reader = bytes.NewReader(nil)
		}
		req, _ := http.NewRequest(method, base+path, reader)
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		} else {
			req.Header.Set("Sec-Fetch-Site", "same-origin")
		}
		client := browser
		if token != "" {
			client = &http.Client{Timeout: 30 * time.Second}
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if out != nil {
			_ = json.NewDecoder(res.Body).Decode(out)
		}
		return res.StatusCode
	}
	email := fmt.Sprintf("e2e-%d@example.com", time.Now().UnixNano())
	if s := call("", "POST", "/api/v1/auth/register", map[string]any{"email": email, "password": "correct-horse-battery"}, nil); s != 201 {
		t.Fatalf("register: %d", s)
	}
	var token struct {
		Secret string `json:"secret"`
	}
	if s := call("", "POST", "/api/v1/tokens", map[string]any{"name": "e2e"}, &token); s != 201 {
		t.Fatalf("token: %d", s)
	}
	t.Setenv("FINALECHAT_TOKEN", token.Secret)

	project := t.TempDir()
	if err := writeJSONAtomic(config.File(project), map[string]any{"finalechat": map[string]any{"base_url": base}}); err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(store.Root(project), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_ = sess.Close()
	external := "eagent:" + sess.ID
	if s := call(token.Secret, "POST", "/api/v1/threads", map[string]any{"external_id": external, "title": "e2e"}, nil); s != 201 {
		t.Fatalf("thread: %d", s)
	}
	var applied atomic.Int32
	owner, err := runtimecontrol.New(project, sess.ID, runtimecontrol.FromConfig(config.Defaults()), func(_ runtimecontrol.Request, v runtimecontrol.Values, _ map[string]any) error {
		applied.Store(int32(v.TaskConcurrency))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var ownerMu sync.Mutex
	ownerOpen := true
	go func() {
		for ctx.Err() == nil {
			ownerMu.Lock()
			if ownerOpen {
				_ = owner.Poll()
			}
			ownerMu.Unlock()
			time.Sleep(20 * time.Millisecond)
		}
	}()
	stop := StartConnector(ctx, project, t.Logf)
	defer stop()

	type link struct {
		ID        string `json:"id"`
		Scope     string `json:"scope"`
		Available bool   `json:"available"`
	}
	links := func() []link {
		var out struct {
			Resources []link `json:"resources"`
		}
		call("", "GET", "/api/v1/threads/ext:"+external+"/settings", nil, &out)
		return out.Resources
	}
	waitFor := func(what string, ok func([]link) bool) []link {
		t.Helper()
		for {
			if l := links(); ok(l) {
				return l
			}
			select {
			case <-ctx.Done():
				t.Fatalf("timed out waiting for %s; thread settings = %+v", what, links())
			case <-time.After(250 * time.Millisecond):
			}
		}
	}
	got := waitFor("the session's settings ahead of the project's", func(l []link) bool {
		return len(l) == 2 && l[0].Scope == "session" && l[0].Available && l[1].Scope == "project"
	})

	// A Save in the thread reaches the running harness.
	var view struct {
		Resource struct {
			Generation string `json:"generation"`
			Descriptor struct {
				SchemaVersion string `json:"schema_version"`
			} `json:"descriptor"`
			Snapshot struct {
				Version string `json:"version"`
			} `json:"snapshot"`
		} `json:"resource"`
	}
	call("", "GET", "/api/v1/settings-resources/"+got[0].ID, nil, &view)
	proposal := map[string]any{"operation": "settings.apply", "schema_version": view.Resource.Descriptor.SchemaVersion, "expected_version": view.Resource.Snapshot.Version, "generation": view.Resource.Generation,
		"edits": []map[string]any{{"op": "set", "key": "/task_concurrency", "value": 5}}}
	var created struct {
		Command struct {
			ID     string         `json:"id"`
			Status string         `json:"status"`
			Result map[string]any `json:"result"`
		} `json:"command"`
	}
	if s := call("", "POST", "/api/v1/settings-resources/"+got[0].ID+"/commands", map[string]any{"client_key": "e2e-1", "proposal": proposal}, &created); s != 202 && s != 201 && s != 200 {
		t.Fatalf("command: %d %+v", s, created)
	}
	for created.Command.Status == "queued" || created.Command.Status == "executing" {
		time.Sleep(250 * time.Millisecond)
		call("", "GET", "/api/v1/commands/"+created.Command.ID, nil, &created)
		if ctx.Err() != nil {
			t.Fatal("command never finished")
		}
	}
	if created.Command.Status != "succeeded" || applied.Load() != 5 {
		t.Fatalf("command %s (%v); harness saw task_concurrency=%d", created.Command.Status, created.Command.Result, applied.Load())
	}

	// The session stops: its settings leave the thread; the project's stay.
	ownerMu.Lock()
	ownerOpen = false
	_ = owner.Close()
	ownerMu.Unlock()
	waitFor("the stopped session's settings to leave the thread", func(l []link) bool {
		return len(l) == 1 && l[0].Scope == "project"
	})
}
