package archive

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/ericflo/eagent/internal/protocol/artifact"
	"github.com/ericflo/eagent/internal/settings"
	webstatic "github.com/ericflo/eagent/internal/web/static"
)

// SettingsWebsite is an integration-owned settings artifact. It contains no
// session transcript, attachments, credentials or process-control files.
func SettingsWebsite(project, session, version string, view settings.RemoteView) (*Export, error) {
	dir, err := os.MkdirTemp("", "eagent-settings-site-")
	if err != nil {
		return nil, err
	}
	out := &Export{Dir: dir, Manifest: artifact.Manifest{Format: artifact.Format,
		Producer: artifact.Producer{Name: "eagent", Version: version}, Entrypoint: "settings/index.html", SettingsEntrypoint: "settings/index.html",
		CapturedAt: time.Now().UTC(), Dataset: map[string]any{"format": "eagent.settings/v1", "session_id": session, "settings_version": view.Snapshot.Version}}}
	ok := false
	defer func() {
		if !ok {
			out.Close()
		}
	}()
	data, err := (&settings.Service{Project: project}).EditorData()
	if err != nil {
		return nil, err
	}
	if err = out.addJSON("settings/state.json", "context", view); err != nil {
		return nil, err
	}
	if err = out.addJSON("settings/editor.json", "context", data); err != nil {
		return nil, err
	}
	page, err := assets.ReadFile("assets/settings.html")
	if err != nil {
		return nil, err
	}
	sdk, err := assets.ReadFile("assets/finale-artifact.js")
	if err != nil {
		return nil, err
	}
	page = bytes.Replace(page, []byte("/* FINALE_ARTIFACT_SDK */"), sdk, 1)
	for marker, name := range map[string]string{"/* EAGENT_EDITOR_STYLE */": "style.css", "/* EAGENT_CONFIG_EDITOR */": "config.js", "/* EAGENT_SETTINGS_TRANSPORT */": "artifact-settings.js"} {
		b, err := webstatic.Files.ReadFile(name)
		if err != nil {
			return nil, err
		}
		page = bytes.Replace(page, []byte(marker), b, 1)
	}
	if err = out.addBytes("settings/index.html", "viewer", "text/html; charset=utf-8", page); err != nil {
		return nil, err
	}
	if err = out.Manifest.Validate(); err != nil {
		return nil, err
	}
	raw, err := json.MarshalIndent(out.Manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	if err = os.WriteFile(filepath.Join(dir, "manifest.json"), raw, 0600); err != nil {
		return nil, err
	}
	ok = true
	return out, nil
}
