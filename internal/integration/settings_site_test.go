package integration

import (
	"context"
	"encoding/json"
	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/finalechat"
	"github.com/ericflo/eagent/internal/settings"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSettingsSitePublishesWithoutBackupAndRecoversLostAcknowledgement(t *testing.T) {
	f, project, session := newPublicationFixture(t)
	cfg, err := config.Load(project, "")
	if err != nil {
		t.Fatal(err)
	}
	// Existing publisher fixture only accepts the account credential. Bindings
	// are exercised separately and must use the scoped credential.
	account := &finalechat.Client{BaseURL: cfg.Finalechat.BaseURL, Token: "fc_fixture"}
	bindings := 0
	binding := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fcc_fixture" {
			t.Error("wrong binding credential")
			w.WriteHeader(403)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["resource_id"] != "resource" || body["revision_id"] == nil {
			t.Error("missing website binding")
		}
		bindings++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer binding.Close()
	connector := &finalechat.Client{BaseURL: binding.URL, Token: "fcc_fixture"}
	if err := writeJSONAtomic(config.File(project), map[string]any{"finalechat": map[string]any{"artifacts": false, "base_url": cfg.Finalechat.BaseURL}}); err != nil {
		t.Fatal(err)
	}
	service := &settings.Service{Project: project}
	view, err := service.RemoteSnapshot(service.Grant())
	if err != nil {
		t.Fatal(err)
	}
	thread := settingsThread{ID: "thread", Session: session.ID, External: "eagent:" + session.ID}
	publish := func() error {
		return publishSettingsSite(context.Background(), project, "test", thread, view, account, connector, "/api/v1/connectors/connector", "resource")
	}
	f.failAfter = true
	_ = publish()
	if err := publish(); err != nil {
		t.Fatal(err)
	}
	if err := publish(); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.commits != 1 {
		t.Fatalf("repeated identical settings created %d revisions", f.commits)
	}
	if bindings < 2 {
		t.Fatal("current editor not bound")
	}
	if f.head.Revision.Manifest.CapturedAt.Before(time.Now().Add(-time.Hour)) {
		t.Fatal("lost real capture time")
	}
	for _, file := range f.head.Revision.Manifest.Files {
		if file.Role == "source" {
			t.Fatal("settings publication uploaded transcript")
		}
	}
}
