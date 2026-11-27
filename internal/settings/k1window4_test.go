package settings

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/config"
)

// The reader's cases for d7f7b75 (kr-wire rv15_window_test.go), as written.

// A client that does not send "loaded" (a script on PUT /api/config), with
// a form read before the server's list changed (32k, then 64k), saves
// another field under EAGENT_BASE_URL. The user entered no window; one is
// written and pinned.
func TestRV15CAStaleFormWithoutLoadedWritesNoWindow(t *testing.T) {
	project, _ := rv15b(t)
	cfg := config.Resolve(project, "", "").Effective
	config.UpdateLiveCacheForServer(project, "local", []config.Model{{Provider: "local", ID: "zai-org/GLM-5.3", Context: 65_536}}, "")
	cfg.TickSeconds = 15
	raw, _ := json.Marshal(cfg)
	res := rv15bSave(t, project, Request{Config: raw})
	file, _ := os.ReadFile(config.File(project))
	if strings.Contains(string(file), "context_tokens") {
		t.Fatalf("window in force after save %d; file = %s; without the variable: %d", res.Resolution.Effective.Model.ContextTokens, file, rv15bUnsetLoad(t, project).Model.ContextTokens)
	}
}

// The same with "loaded" sent and a refresh that failed (the server's list
// emptied, servers.go:710) between load and save.
func TestRV15CAFailedRefreshWithLoadedWritesNoWindow(t *testing.T) {
	project, _ := rv15b(t)
	cfg := config.Resolve(project, "", "").Effective
	loaded, _ := json.Marshal(cfg)
	config.UpdateLiveCacheForServer(project, "local", nil, "connection refused")
	cfg.TickSeconds = 15
	raw, _ := json.Marshal(cfg)
	rv15bSave(t, project, Request{Config: raw, Loaded: loaded})
	file, _ := os.ReadFile(config.File(project))
	if strings.Contains(string(file), "context_tokens") {
		t.Fatalf("file = %s", file)
	}
}

// Without "loaded", the save says why the window the request carries was
// not written: it follows the server EAGENT_BASE_URL names.
func TestK1AWindowNotWrittenWithoutLoadedIsReported(t *testing.T) {
	project, _ := rv15b(t)
	cfg := config.Resolve(project, "", "").Effective
	cfg.Model.ContextTokens = 16_000
	raw, _ := json.Marshal(cfg)
	res := rv15bSave(t, project, Request{Config: raw})
	file, _ := os.ReadFile(config.File(project))
	found := false
	for _, f := range res.Skipped {
		found = found || f.Pointer == "/model/context_tokens" && f.Env == "EAGENT_BASE_URL"
	}
	if strings.Contains(string(file), "context_tokens") || !found {
		t.Fatalf("skipped %+v, file = %s", res.Skipped, file)
	}
}
