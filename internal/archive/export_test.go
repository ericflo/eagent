package archive

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/protocol/artifact"
	"github.com/ericflo/eagent/internal/store"
)

func TestSnapshotPreservesNativeBytesAndCommittedBoundary(t *testing.T) {
	project := t.TempDir()
	session, err := store.Create(store.Root(project), time.UnixMilli(1788800000000))
	if err != nil {
		t.Fatal(err)
	}
	_, err = session.Append(event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = session.Append(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "hello 猫"}))
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(session.Path)
	if err != nil {
		t.Fatal(err)
	}
	var logPath string
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".jsonl" {
			logPath = filepath.Join(session.Path, e.Name())
		}
	}
	original, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, append(append([]byte{}, original...), []byte("{\"seq\":999")...), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(session.Path, "outputs"), 0o700); err != nil {
		t.Fatal(err)
	}
	asset := bytes.Repeat([]byte{0, 1, 2, 255}, 400000)
	if err := os.WriteFile(filepath.Join(session.Path, "outputs", "full.bin"), asset, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(config.File(project)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.File(project), []byte("{\"unknown_secret\":\"do-not-publish-this-value\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	exported, err := Snapshot(context.Background(), project, session.ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer exported.Close()
	if err := exported.Manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(exported.Dir, "sessions", filepath.Base(logPath)))
	if err != nil || !bytes.Equal(got, original) {
		t.Fatal("source was reserialized or included a torn tail", err)
	}
	got, err = os.ReadFile(filepath.Join(exported.Dir, "outputs/full.bin"))
	if err != nil || !bytes.Equal(got, asset) {
		t.Fatal("asset changed", err)
	}
	for _, f := range exported.Manifest.Files {
		raw, err := os.ReadFile(filepath.Join(exported.Dir, filepath.FromSlash(f.Path)))
		if err != nil {
			t.Fatal(err)
		}
		if artifact.Digest(raw) != f.SHA256 {
			t.Fatalf("bad whole-file hash: %s", f.Path)
		}
		offset := int64(0)
		for _, c := range f.Chunks {
			if artifact.Digest(raw[offset:offset+c.Size]) != c.SHA256 {
				t.Fatal("bad chunk")
			}
			offset += c.Size
		}
		if bytes.Contains(raw, []byte("do-not-publish-this-value")) {
			t.Fatal("unknown config secret leaked")
		}
		if f.Path == ".lock" || f.Path == "inbox" {
			t.Fatal("process-control file archived")
		}
	}
	if exported.Detail.LastSeq != 2 || exported.Detail.FirstMessage != "hello 猫" {
		t.Fatalf("wrong projection: %+v", exported.Detail)
	}
	var m artifact.Manifest
	raw, _ := os.ReadFile(filepath.Join(exported.Dir, "manifest.json"))
	if json.Unmarshal(raw, &m) != nil || m.Validate() != nil {
		t.Fatal("bad portable manifest")
	}
}

func TestSnapshotRefusesEscapingAsset(t *testing.T) {
	project := t.TempDir()
	session, err := store.Create(store.Root(project), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = session.Append(event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{}))
	_ = session.Close()
	if err := os.MkdirAll(filepath.Join(session.Path, "attachments"), 0o700); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(t.TempDir(), "private.txt")
	_ = os.WriteFile(secret, []byte("private"), 0o600)
	if err := os.Symlink(secret, filepath.Join(session.Path, "attachments", "outside.txt")); err != nil {
		t.Fatal(err)
	}
	if e, err := Snapshot(context.Background(), project, session.ID, "test"); err == nil {
		e.Close()
		t.Fatal("symlink asset accepted")
	}
}
