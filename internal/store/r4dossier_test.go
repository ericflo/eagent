package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
)

// A run that stopped after making the next subsession's file and before
// writing anything to it leaves an empty file at the end of the record.
// Opening the session lets it go and appends where the record goes on.
func TestOpenLetsGoOfAnEmptyNextFile(t *testing.T) {
	s, err := Create(t.TempDir(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	first := s.Current()
	if _, err := s.Append(event.New(event.SubsessionStart, event.ActorHarness, event.SubsessionStartData{File: first, Reason: "new"})); err != nil {
		t.Fatal(err)
	}
	next, err := s.CreateSubsession(time.Now().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	// Stopped here: the file is on disk and never written.
	_ = s.pending.Close()
	s.pending = nil
	s.Close()
	if _, err := os.Stat(filepath.Join(s.Path, next)); err != nil {
		t.Fatalf("the next file is not on disk: %v", err)
	}
	o, _, err := Open(s.Path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	if o.Current() != first {
		t.Fatalf("appending to %s, want %s", o.Current(), first)
	}
	if _, err := os.Stat(filepath.Join(s.Path, next)); !os.IsNotExist(err) {
		t.Fatalf("the empty next file is still there: %v", err)
	}
	ev, err := o.Append(event.New(event.SessionResume, event.ActorHarness, event.SessionResumeData{}))
	if err != nil || ev.Source.File != first || ev.Source.Line != 2 {
		t.Fatalf("appended at %v (%v)", ev.Source, err)
	}
}

// The only file of a new session is kept even when empty.
func TestOpenKeepsAnEmptyOnlyFile(t *testing.T) {
	s, err := Create(t.TempDir(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	name := s.Current()
	s.Close()
	o, _, err := Open(s.Path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	if o.Current() != name {
		t.Fatalf("appending to %q, want %q", o.Current(), name)
	}
}

// The record is read in the order its file names sort, so the next file is
// named after the current one even when the clock was set back.
func TestNextFileSortsAfterTheCurrentWhenTheClockWentBack(t *testing.T) {
	s, err := Create(t.TempDir(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	first := s.Current()
	next, err := s.CreateSubsession(time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if next <= first {
		t.Fatalf("the next file %s sorts before the current %s", next, first)
	}
	if _, err := s.OpenSubsession(next); err != nil {
		t.Fatal(err)
	}
	name, err := s.NewSubsessionName(time.Now().Add(-time.Hour))
	if err != nil || name <= next {
		t.Fatalf("NewSubsessionName gave %s after %s (%v)", name, next, err)
	}
}
