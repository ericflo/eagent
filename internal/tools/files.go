// Package tools implements the harness-side half of the actors' tools: file
// access confined to the project, output truncation, and the session
// archive tools that let the task worker build a dossier with citations.
package tools

import (
	"errors"
	"fmt"
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

// Resolve turns a model-supplied path into an absolute path inside the
// project (or the OS temp dir). Escapes are rejected with a clear message.
func (f Files) Resolve(p string) (string, error) {
	if strings.TrimSpace(p) == "" {
		return "", errors.New("path is required")
	}
	abs := p
	if !filepath.IsAbs(p) {
		abs = filepath.Join(f.Root, p)
	}
	abs = filepath.Clean(abs)
	if f.AllowOutside {
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
	return "", fmt.Errorf("%s is outside the project directory %s; use a path inside the project (or a shell command if you really mean it)", p, root)
}

// ReadFile returns a file's contents, optionally a window of lines.
func (f Files) ReadFile(p string, offset, limit int, maxBytes int) (string, error) {
	abs, err := f.Resolve(p)
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
	raw, err := os.ReadFile(abs)
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
	abs, err := f.Resolve(p)
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
	abs, err := f.Resolve(p)
	if err != nil {
		return "", err
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
	abs, err := f.Resolve(p)
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
	return s[:head] + fmt.Sprintf("\n\n[... %d characters omitted; %d total ...]\n\n", omitted, len(s)) + s[tailStart:]
}
