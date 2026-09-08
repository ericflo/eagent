package archive

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/store"
)

func recoveryFixture(t *testing.T) *Export {
	t.Helper()
	project := t.TempDir()
	session, err := store.Create(store.Root(project), time.UnixMilli(1788800000000))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Append(event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{})); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Append(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "recover π 🐈"})); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	exported, err := Snapshot(context.Background(), project, session.ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(exported.Close)
	return exported
}

func TestVerifiedRecoveryPreservesNativeBytesOnly(t *testing.T) {
	exported := recoveryFixture(t)
	if _, err := Verify(context.Background(), exported.Dir); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "recovered")
	n, err := Recover(context.Background(), exported.Dir, destination)
	if err != nil || n == 0 {
		t.Fatalf("recovery: count=%d error=%v", n, err)
	}
	for _, file := range exported.Manifest.Files {
		restored, err := os.ReadFile(filepath.Join(destination, file.Path))
		if file.Role != "source" {
			if !os.IsNotExist(err) {
				t.Fatalf("restored non-source file: %s", file.Path)
			}
			continue
		}
		original, readErr := os.ReadFile(filepath.Join(exported.Dir, file.Path))
		if err != nil || readErr != nil || !bytes.Equal(original, restored) {
			t.Fatalf("native bytes changed: %s", file.Path)
		}
	}
	if _, err := Recover(context.Background(), exported.Dir, destination); err == nil {
		t.Fatal("existing destination was overwritten")
	}
}

func TestRecoveryRejectsCorruptionBeforeCreatingDestination(t *testing.T) {
	exported := recoveryFixture(t)
	for _, file := range exported.Manifest.Files {
		if file.Role != "source" {
			continue
		}
		path := filepath.Join(exported.Dir, file.Path)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		raw[0] ^= 1
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		break
	}
	destination := filepath.Join(t.TempDir(), "recovered")
	if _, err := Recover(context.Background(), exported.Dir, destination); err == nil {
		t.Fatal("corrupt source was recovered")
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatal("invalid archive created a recovery destination")
	}
}

func TestVerifyRefusesSymlinksAndCancelledReads(t *testing.T) {
	exported := recoveryFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Verify(ctx, exported.Dir); err == nil {
		t.Fatal("cancelled verification succeeded")
	}
	path := filepath.Join(exported.Dir, "index.html")
	outside := filepath.Join(t.TempDir(), "outside.html")
	if err := os.Rename(path, outside); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(context.Background(), exported.Dir); err == nil {
		t.Fatal("symlink was followed during verification")
	}
}
