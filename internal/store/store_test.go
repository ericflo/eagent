package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
)

func TestCreateAppendRead(t *testing.T) {
	root := t.TempDir()
	s, err := Create(root, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := s.Append(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "hi"})); err != nil {
			t.Fatal(err)
		}
	}
	if s.LastSeq() != 3 {
		t.Fatalf("seq = %d", s.LastSeq())
	}
	next, err := s.NewSubsession(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(event.New(event.Note, event.ActorOrchestrator, event.NoteData{Text: "n"})); err != nil {
		t.Fatal(err)
	}
	s.Close()

	evs, err := Read(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 4 {
		t.Fatalf("read %d events", len(evs))
	}
	for i, ev := range evs {
		if ev.Seq != int64(i+1) {
			t.Fatalf("event %d has seq %d", i, ev.Seq)
		}
	}
	if evs[3].Source.File != next || evs[3].Source.Line != 1 {
		t.Fatalf("source = %+v", evs[3].Source)
	}
	files, _ := s.Files()
	if len(files) != 2 || files[0] >= files[1] {
		t.Fatalf("files = %v", files)
	}
}

func TestTornTailIsSkippedAndRepaired(t *testing.T) {
	root := t.TempDir()
	s, err := Create(root, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	s.Append(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "one"}))
	s.Close()
	files, _ := s.Files()
	path := filepath.Join(s.Path, files[0])
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString(`{"seq":2,"type":"user.mess`) // crash mid-write
	f.Close()

	evs, err := Read(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 {
		t.Fatalf("expected the torn line to be skipped, got %d events", len(evs))
	}
	s2, err := Open(s.Path, 1, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s2.Append(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "two"})); err != nil {
		t.Fatal(err)
	}
	s2.Close()
	evs, err = Read(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 2 || evs[1].Seq != 2 {
		t.Fatalf("after repair: %d events, last seq %d", len(evs), evs[len(evs)-1].Seq)
	}
	if _, err := os.Stat(path + ".torn"); err != nil {
		t.Fatalf("torn bytes not preserved: %v", err)
	}
}

func TestLockPreventsSecondRunner(t *testing.T) {
	root := t.TempDir()
	s, err := Create(root, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := Open(s.Path, 0, time.Now()); err == nil {
		t.Fatal("second Open should fail while the first holds the lock")
	}
}

func TestResolve(t *testing.T) {
	root := t.TempDir()
	a, _ := Create(root, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	a.Close()
	b, _ := Create(root, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	b.Close()
	got, err := Resolve(root, "latest")
	if err != nil || got.ID != b.ID {
		t.Fatalf("latest = %v, %v", got.ID, err)
	}
	got, err = Resolve(root, a.ID[:8])
	if err != nil || got.ID != a.ID {
		t.Fatalf("prefix = %v, %v", got.ID, err)
	}
	if _, err := Resolve(root, "nope"); err == nil {
		t.Fatal("expected no match")
	}
}
