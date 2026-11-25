package store

import (
	"os"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
)

// readOnly makes dir refuse new files until the test ends. Root ignores
// the mode, so the tests that need it are skipped there.
func readOnly(t *testing.T, dir string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("a directory cannot be made read-only for root")
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
}

// A session that cannot create its next file goes on appending to the one
// it has. Before, the current file was closed first, and every later
// append failed with "session is not open for writing".
func TestSessionKeepsItsFileWhenTheNextCannotBeCreated(t *testing.T) {
	s, err := Create(t.TempDir(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cur := s.Current()
	if _, err := s.Append(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "one"})); err != nil {
		t.Fatal(err)
	}
	readOnly(t, s.Path)
	if _, err := s.CreateSubsession(time.Now().Add(time.Second)); err == nil {
		t.Fatal("a file was created in a directory that refuses new files")
	}
	if _, err := s.OpenSubsession("2099-01-01T00-00-00-000.jsonl"); err == nil {
		t.Fatal("a file was opened in a directory that refuses new files")
	}
	ev, err := s.Append(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "two"}))
	if err != nil {
		t.Fatalf("the session lost its file: %v", err)
	}
	if s.Current() != cur || ev.Source.File != cur || ev.Source.Line != 2 {
		t.Fatalf("the event went to %s:%d, want %s:2", ev.Source.File, ev.Source.Line, cur)
	}
}

// The next file exists from CreateSubsession on, takes nothing until the
// session is switched to it, and is the file OpenSubsession then writes.
func TestCreateSubsessionThenOpenIt(t *testing.T) {
	s, err := Create(t.TempDir(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	first := s.Current()
	next, err := s.CreateSubsession(time.Now().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if next == first {
		t.Fatalf("the next file has the current one's name %s", next)
	}
	end, err := s.Append(event.New(event.SubsessionEnd, event.ActorHarness, event.SubsessionEndData{Reason: "context full", NextFile: next}))
	if err != nil || end.Source.File != first {
		t.Fatalf("the closing event went to %s (%v), want %s", end.Source.File, err, first)
	}
	if _, err := s.OpenSubsession(next); err != nil {
		t.Fatal(err)
	}
	start, err := s.Append(event.New(event.SubsessionStart, event.ActorHarness, event.SubsessionStartData{File: next, Index: 1, Reason: "rollover"}))
	if err != nil || start.Source.File != next || start.Source.Line != 1 {
		t.Fatalf("the opening event went to %s:%d (%v), want %s:1", start.Source.File, start.Source.Line, err, next)
	}
	files, err := s.Files()
	if err != nil || len(files) != 2 || files[0] != first || files[1] != next {
		t.Fatalf("files = %v (%v)", files, err)
	}
}

// A file that was created and never switched to holds nothing, and does
// not stay behind as an empty last file of the record.
func TestCreatedSubsessionThatIsNeverOpenedIsRemoved(t *testing.T) {
	s, err := Create(t.TempDir(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	first := s.Current()
	if _, err := s.CreateSubsession(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	path := s.Path
	s.Close()
	files, err := subsessionFiles(path)
	if err != nil || len(files) != 1 || files[0] != first {
		t.Fatalf("files after close = %v (%v), want only %s", files, err, first)
	}
}
