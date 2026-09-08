// Package store persists sessions as directories of append-only JSONL files.
//
// Layout, relative to the project directory:
//
//	.agents/eagent/sessions/<session>/<epoch-ms>.jsonl
//
// Each file is one subsession. File names are zero-padded epoch milliseconds,
// so lexicographic order is chronological order and replay is "list the files,
// read the lines".
package store

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ericflo/eagent/internal/event"
)

// Dir is the per-project root that holds all sessions.
const Dir = ".agents/eagent/sessions"

// Root locates the sessions directory for a project.
func Root(project string) string { return filepath.Join(project, Dir) }

// NewID returns a sortable session or subsession id for the current time.
func NewID(now time.Time) string {
	return fmt.Sprintf("%013d", now.UnixMilli())
}

// Info summarises a session directory without loading it.
type Info struct {
	ID          string
	Path        string
	Started     time.Time
	Modified    time.Time
	Subsessions int
	Size        int64
}

// List returns the sessions under root, oldest first.
func List(root string) ([]Info, error) {
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Info
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info := Info{ID: e.Name(), Path: filepath.Join(root, e.Name())}
		files, _ := subsessionFiles(info.Path)
		info.Subsessions = len(files)
		for _, f := range files {
			st, err := os.Stat(filepath.Join(info.Path, f))
			if err != nil {
				continue
			}
			info.Size += st.Size()
			if st.ModTime().After(info.Modified) {
				info.Modified = st.ModTime()
			}
		}
		if ms, err := strconv.ParseInt(strings.TrimLeft(e.Name(), "0"), 10, 64); err == nil {
			info.Started = time.UnixMilli(ms)
		} else if st, err := e.Info(); err == nil {
			info.Started = st.ModTime()
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Resolve finds a session by exact id, unique prefix, or "latest".
func Resolve(root, ref string) (Info, error) {
	sessions, err := List(root)
	if err != nil {
		return Info{}, err
	}
	if len(sessions) == 0 {
		return Info{}, fmt.Errorf("no sessions in %s", root)
	}
	if ref == "" || ref == "latest" {
		// The newest session that has something in it: a start that failed
		// before its first event must not shadow the real one.
		for i := len(sessions) - 1; i >= 0; i-- {
			if sessions[i].Size > 0 {
				return sessions[i], nil
			}
		}
		return sessions[len(sessions)-1], nil
	}
	var matches []Info
	for _, s := range sessions {
		if s.ID == ref {
			return s, nil
		}
		if strings.HasPrefix(s.ID, ref) {
			matches = append(matches, s)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return Info{}, fmt.Errorf("no session matches %q", ref)
	default:
		return Info{}, fmt.Errorf("%d sessions match %q; be more specific", len(matches), ref)
	}
}

func subsessionFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)
	return files, nil
}

// Session is an open session directory with one writable subsession file.
type Session struct {
	ID   string
	Path string

	mu      sync.Mutex
	file    *os.File
	current string // current subsession file name
	seq     int64
	lock    *os.File
	closed  bool
	nextSeq func() int64
}

// Create makes a new session directory and its first subsession file.
func Create(root string, now time.Time) (*Session, error) {
	id := NewID(now)
	path := filepath.Join(root, id)
	for i := 0; ; i++ {
		if err := os.MkdirAll(root, 0o755); err != nil {
			return nil, err
		}
		if err := os.Mkdir(path, 0o755); err == nil {
			break
		} else if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		// Two sessions in the same millisecond: bump.
		now = now.Add(time.Millisecond)
		id = NewID(now)
		path = filepath.Join(root, id)
		if i > 1000 {
			return nil, errors.New("could not allocate a session id")
		}
	}
	s := &Session{ID: id, Path: path}
	if err := s.acquireLock(); err != nil {
		return nil, err
	}
	if _, err := s.newSubsession(now); err != nil {
		return nil, err
	}
	return s, nil
}

// Open opens an existing session for appending. It does not read history;
// use Read for that.
func Open(path string, lastSeq int64, now time.Time) (*Session, error) {
	files, err := subsessionFiles(path)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("%s has no subsession files", path)
	}
	s := &Session{ID: filepath.Base(path), Path: path, seq: lastSeq}
	if err := s.acquireLock(); err != nil {
		return nil, err
	}
	current := files[len(files)-1]
	if err := s.repairTail(filepath.Join(path, current)); err != nil {
		s.Close()
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(path, current), os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		s.Close()
		return nil, err
	}
	s.file = f
	s.current = current
	return s, nil
}

// ReadOnly returns a Session handle that can enumerate files but not append.
func ReadOnly(path string) *Session {
	return &Session{ID: filepath.Base(path), Path: path, closed: true}
}

func (s *Session) acquireLock() error {
	lockPath := filepath.Join(s.Path, ".lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	// A liveness probe holds a shared lock for a moment; wait it out before
	// declaring the session taken.
	var lerr error
	for i := 0; i < 50; i++ {
		if lerr = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); lerr == nil {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if lerr != nil {
		f.Close()
		pid, _ := os.ReadFile(lockPath)
		return fmt.Errorf("session %s is already being run (pid %s); stop that runner first or read it with `eagent show`", s.ID, strings.TrimSpace(string(pid)))
	}
	_ = f.Truncate(0)
	_, _ = f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	s.lock = f
	return nil
}

// repairTail drops a final line that was never terminated by a newline (a
// crash mid-write). Committed lines are never touched.
func (s *Session) repairTail(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(data) == 0 || data[len(data)-1] == '\n' {
		return nil
	}
	cut := bytes.LastIndexByte(data, '\n') + 1
	torn := data[cut:]
	// Keep the bytes for forensics, then truncate.
	_ = os.WriteFile(path+".torn", torn, 0o644)
	return os.Truncate(path, int64(cut))
}

// Current returns the name of the subsession file being appended to.
func (s *Session) Current() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current
}

// Files lists the subsession files in chronological order.
func (s *Session) Files() ([]string, error) { return subsessionFiles(s.Path) }

// LastSeq returns the sequence number of the last appended event.
func (s *Session) LastSeq() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seq
}

// Append writes one event, assigning Seq and Time, and fsyncs it.
func (s *Session) Append(ev event.Event) (event.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.file == nil {
		return ev, errors.New("session is not open for writing")
	}
	s.seq++
	ev.Seq = s.seq
	if ev.Time.IsZero() {
		ev.Time = time.Now().UTC()
	}
	line, err := json.Marshal(ev)
	if err != nil {
		s.seq--
		return ev, err
	}
	line = append(line, '\n')
	if _, err := s.file.Write(line); err != nil {
		// A partial line may be on disk; refuse further writes so the torn
		// tail is repaired on the next open rather than glued to a new line.
		s.closed = true
		return ev, err
	}
	if err := s.file.Sync(); err != nil {
		return ev, err
	}
	ev.Source = event.Source{File: s.current}
	return ev, nil
}

// NewSubsession closes the current file and opens the next one.
func (s *Session) NewSubsession(now time.Time) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.newSubsession(now)
}

// NewSubsessionName allocates the next subsession file name without opening
// it, so the closing event of the current file can name its successor.
func (s *Session) NewSubsessionName(now time.Time) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name := NewID(now) + ".jsonl"
	for i := 0; ; i++ {
		if _, err := os.Stat(filepath.Join(s.Path, name)); errors.Is(err, os.ErrNotExist) && name != s.current {
			return name, nil
		}
		now = now.Add(time.Millisecond)
		name = NewID(now) + ".jsonl"
		if i > 1000 {
			return "", errors.New("could not allocate a subsession name")
		}
	}
}

// OpenSubsession closes the current file and starts appending to name.
func (s *Session) OpenSubsession(name string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file != nil {
		_ = s.file.Close()
		s.file = nil
	}
	f, err := os.OpenFile(filepath.Join(s.Path, name), os.O_CREATE|os.O_WRONLY|os.O_APPEND|os.O_EXCL, 0o644)
	if err != nil {
		return "", err
	}
	s.file = f
	s.current = name
	s.closed = false
	return name, nil
}

func (s *Session) newSubsession(now time.Time) (string, error) {
	if s.file != nil {
		_ = s.file.Close()
		s.file = nil
	}
	name := NewID(now) + ".jsonl"
	for {
		if _, err := os.Stat(filepath.Join(s.Path, name)); errors.Is(err, os.ErrNotExist) {
			break
		}
		now = now.Add(time.Millisecond)
		name = NewID(now) + ".jsonl"
	}
	f, err := os.OpenFile(filepath.Join(s.Path, name), os.O_CREATE|os.O_WRONLY|os.O_APPEND|os.O_EXCL, 0o644)
	if err != nil {
		return "", err
	}
	s.file = f
	s.current = name
	s.closed = false
	return name, nil
}

// Close releases the file and lock.
// Discard closes a session and removes its directory. Only for a session
// that never had an event committed: a stub would otherwise shadow the real
// latest session for `-c` and `resume latest`.
func (s *Session) Discard() error {
	path := s.Path
	_ = s.Close()
	return os.RemoveAll(path)
}

func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	var err error
	if s.file != nil {
		err = s.file.Close()
		s.file = nil
	}
	if s.lock != nil {
		// The file stays; removing it would let a runner that opened the old
		// inode lock it while another creates a fresh one.
		_ = syscall.Flock(int(s.lock.Fd()), syscall.LOCK_UN)
		_ = s.lock.Close()
		s.lock = nil
	}
	return err
}

// Read replays every committed event of a session directory in order. A
// torn final line (crash mid-write) is skipped; the caller decides whether
// to repair it by opening the session for writing.
func Read(path string) ([]event.Event, error) {
	files, err := subsessionFiles(path)
	if err != nil {
		return nil, err
	}
	var events []event.Event
	for _, name := range files {
		evs, err := ReadFile(filepath.Join(path, name))
		if err != nil {
			return nil, err
		}
		events = append(events, evs...)
	}
	return events, nil
}

// ReadFile parses one subsession file.
func ReadFile(path string) ([]event.Event, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	name := filepath.Base(path)
	r := bufio.NewReaderSize(f, 1<<20)
	var events []event.Event
	line := 0
	for {
		raw, err := r.ReadBytes('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		if len(raw) == 0 && errors.Is(err, io.EOF) {
			break
		}
		line++
		if errors.Is(err, io.EOF) {
			// Uncommitted tail: ignore.
			break
		}
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) == 0 {
			continue
		}
		var ev event.Event
		if jerr := json.Unmarshal(trimmed, &ev); jerr != nil {
			return nil, fmt.Errorf("%s:%d: corrupt event: %w", name, line, jerr)
		}
		ev.Source = event.Source{File: name, Line: line}
		events = append(events, ev)
	}
	return events, nil
}

// LineCount returns the number of committed lines in a subsession file.
func LineCount(path string) (int, error) {
	evs, err := ReadFile(path)
	if err != nil {
		return 0, err
	}
	return len(evs), nil
}
