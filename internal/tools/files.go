// Package tools implements the harness-side half of the actors' tools: file
// access confined to the project, output truncation, and the session
// archive tools that let the task worker build a dossier with citations.
package tools

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

// Files gives actors project-relative file access.
type Files struct {
	Root         string
	AllowOutside bool
}

// Resolve turns a model-supplied path into an absolute path. Reads may go
// anywhere the process can see; writes are confined to the project directory
// (or the OS temp dir) so a mistyped path cannot clobber something outside
// the project. Escapes are rejected with a clear message.
func (f Files) Resolve(p string, write bool) (string, error) {
	if strings.TrimSpace(p) == "" {
		return "", errors.New("path is required")
	}
	abs := p
	if !filepath.IsAbs(p) {
		abs = filepath.Join(f.Root, p)
	}
	abs = filepath.Clean(abs)
	if f.AllowOutside || !write {
		return abs, nil
	}
	root := filepath.Clean(f.Root)
	if abs == root || strings.HasPrefix(abs, root+string(filepath.Separator)) {
		return abs, nil
	}
	tmp := filepath.Clean(os.TempDir())
	if strings.HasPrefix(abs, tmp+string(filepath.Separator)) {
		return abs, nil
	}
	return "", fmt.Errorf("refusing to write %s: it is outside the project directory %s. Write inside the project (or use a shell command if you really mean it)", p, root)
}

// ReadFile returns a file's contents, optionally a window of lines.
func (f Files) ReadFile(p string, offset, limit int, maxBytes int) (string, error) {
	abs, err := f.Resolve(p, false)
	if err != nil {
		return "", err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if st.IsDir() {
		return f.ListDir(p)
	}
	if err := readable(p, st); err != nil {
		return "", err
	}
	fh, err := os.Open(abs)
	if err != nil {
		return "", err
	}
	defer fh.Close()
	// Bounded even if the file grows while it is read (an active log).
	raw, err := io.ReadAll(io.LimitReader(fh, readCap))
	if err != nil {
		return "", err
	}
	if !utf8.Valid(raw) {
		return fmt.Sprintf("(binary file, %d bytes)", len(raw)), nil
	}
	content := string(raw)
	lines := strings.Split(content, "\n")
	total := len(lines)
	if offset > 0 || limit > 0 {
		start := max(offset-1, 0)
		if start > total {
			start = total
		}
		end := total
		if limit > 0 && start+limit < end {
			end = start + limit
		}
		window := strings.Join(lines[start:end], "\n")
		header := fmt.Sprintf("[%s lines %d-%d of %d]\n", p, start+1, end, total)
		return header + window, nil
	}
	if maxBytes > 0 && len(content) > maxBytes {
		cut := maxBytes
		for cut > 0 && !utf8.RuneStart(content[cut]) {
			cut--
		}
		shown := strings.Count(content[:cut], "\n")
		return content[:cut] + fmt.Sprintf("\n[... truncated: showing %d of %d lines (%d of %d bytes). Use offset/limit to read the rest.]", shown, total, cut, len(content)), nil
	}
	return content, nil
}

// WriteFile creates or replaces a file, making parent directories.
func (f Files) WriteFile(p, content string) (string, error) {
	abs, err := f.Resolve(p, true)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", err
	}
	existed := false
	if _, err := os.Stat(abs); err == nil {
		existed = true
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		return "", err
	}
	verb := "created"
	if existed {
		verb = "overwrote"
	}
	return fmt.Sprintf("%s %s (%d bytes, %d lines)", verb, p, len(content), strings.Count(content, "\n")+boolInt(content != "" && !strings.HasSuffix(content, "\n"))), nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// EditFile replaces one exact occurrence of old with new.
func (f Files) EditFile(p, oldText, newText string, replaceAll bool) (string, error) {
	abs, err := f.Resolve(p, true)
	if err != nil {
		return "", err
	}
	if st, err := os.Stat(abs); err == nil {
		if err := readable(p, st); err != nil {
			return "", err
		}
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	if oldText == "" {
		return "", errors.New("old_text must not be empty (use write_file to create content)")
	}
	content := string(raw)
	count := strings.Count(content, oldText)
	switch {
	case count == 0:
		return "", fmt.Errorf("old_text not found in %s. Read the file and copy the exact text, including whitespace", p)
	case count > 1 && !replaceAll:
		return "", fmt.Errorf("old_text occurs %d times in %s; include more surrounding context to make it unique, or set replace_all", count, p)
	}
	var updated string
	if replaceAll {
		updated = strings.ReplaceAll(content, oldText, newText)
	} else {
		updated = strings.Replace(content, oldText, newText, 1)
	}
	if err := os.WriteFile(abs, []byte(updated), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("edited %s (%d replacement%s)", p, count, plural(count)), nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// ListDir renders a directory listing, skipping noisy trees.
func (f Files) ListDir(p string) (string, error) {
	if p == "" {
		p = "."
	}
	abs, err := f.Resolve(p, false)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return "", err
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir() != entries[j].IsDir() {
			return entries[i].IsDir()
		}
		return entries[i].Name() < entries[j].Name()
	})
	var b strings.Builder
	for _, e := range entries {
		if e.IsDir() {
			fmt.Fprintf(&b, "%s/\n", e.Name())
			continue
		}
		info, err := e.Info()
		if err != nil {
			fmt.Fprintf(&b, "%s\n", e.Name())
			continue
		}
		fmt.Fprintf(&b, "%s  (%d bytes)\n", e.Name(), info.Size())
	}
	if b.Len() == 0 {
		return "(empty directory)", nil
	}
	return b.String(), nil
}

// Truncate bounds text to about max characters, keeping the head and the
// tail, without splitting a UTF-8 sequence.
func Truncate(s string, max int) string {
	return TruncateWithNotice(s, max, "")
}

// TruncateWithNotice is Truncate with an extra sentence in the marker, used
// to tell the model where the full text was saved.
func TruncateWithNotice(s string, max int, notice string) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	head := max * 2 / 3
	tail := max - head
	for head > 0 && !utf8.RuneStart(s[head]) {
		head--
	}
	tailStart := len(s) - tail
	for tailStart < len(s) && !utf8.RuneStart(s[tailStart]) {
		tailStart++
	}
	omitted := tailStart - head
	marker := fmt.Sprintf("[... %d characters omitted; %d total", omitted, len(s))
	if notice != "" {
		marker += ". " + notice
	}
	return s[:head] + "\n\n" + marker + " ...]\n\n" + s[tailStart:]
}

// Spill saves text under dir with the given base name and returns the path.
func Spill(dir, base, text string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	// The base comes from a tool-call id, which the endpoint chose and which
	// can hold anything; it is reduced to one harmless file name first.
	path := filepath.Join(dir, safeBase(base)+".txt")
	return path, os.WriteFile(path, []byte(text), 0o644)
}

// safeBase keeps [A-Za-z0-9._-] and folds everything else to "_", so the
// result is a single path element with no separator and no traversal.
func safeBase(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.TrimLeft(b.String(), ".")
	if len(out) > 96 {
		out = out[:96]
	}
	if out == "" {
		var h uint64 = 14695981039346656037
		for i := 0; i < len(s); i++ {
			h = (h ^ uint64(s[i])) * 1099511628211
		}
		out = fmt.Sprintf("out-%x", h)
	}
	return out
}

// readCap is the most bytes a file tool pulls into memory. Larger files are
// read in parts with shell tools; a device or a pipe is never read at all.
const readCap = 64 << 20

// readable refuses what must not be read whole: anything that is not a
// regular file (a device would never end, a pipe with no writer would hang
// the actor forever) and anything over readCap (one os.ReadFile of a
// multi-gigabyte artifact took the whole process down).
func readable(p string, st os.FileInfo) error {
	if !st.Mode().IsRegular() {
		return fmt.Errorf("refusing to read %s: it is not a regular file (%s); use a shell command if you really mean it", p, st.Mode().Type())
	}
	if st.Size() > readCap {
		return fmt.Errorf("%s is %d bytes, too large to read into memory (limit %d); use bash (head, tail, sed, grep) to look at parts of it", p, st.Size(), readCap)
	}
	return nil
}
