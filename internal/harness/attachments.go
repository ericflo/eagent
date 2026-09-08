package harness

import (
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/finalechat"
)

// Attachments are files that travel with messages: screenshots the user
// sends from their phone, and files the narrator sends back. Every one is
// copied under <session>/attachments/ so the log points at bytes that will
// still be there on replay, whatever happens to the project directory.

// attachmentsDir is where a session keeps message files.
func attachmentsDir(sessionPath string) string { return filepath.Join(sessionPath, "attachments") }

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// safeName reduces a filename to something a shell and a browser both like.
func safeName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	name = unsafeName.ReplaceAllString(name, "_")
	name = strings.Trim(name, "._")
	if name == "" {
		name = "file"
	}
	if len(name) > 120 {
		name = name[len(name)-120:]
	}
	return name
}

// contentTypeFor guesses a file's type from its extension, then its bytes.
func contentTypeFor(name string, head []byte) string {
	if mt := mime.TypeByExtension(strings.ToLower(filepath.Ext(name))); mt != "" {
		if i := strings.IndexByte(mt, ';'); i > 0 {
			mt = mt[:i]
		}
		return mt
	}
	if len(head) > 0 {
		mt := http.DetectContentType(head)
		if i := strings.IndexByte(mt, ';'); i > 0 {
			mt = mt[:i]
		}
		return mt
	}
	return "application/octet-stream"
}

func kindFor(contentType string) string {
	switch contentType {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return "image"
	}
	return "file"
}

// narratorAttachments validates the paths a narrator wants to send and
// copies each into the session so the log stays self-contained. Errors are
// written for the narrator to read.
func (r *Runtime) narratorAttachments(raw any) ([]event.Attachment, error) {
	list, _ := raw.([]any)
	if len(list) == 0 {
		return nil, nil
	}
	if len(list) > finalechat.MaxAttachments {
		return nil, fmt.Errorf("at most %d attachments per message; you gave %d", finalechat.MaxAttachments, len(list))
	}
	var out []event.Attachment
	for _, item := range list {
		p, _ := item.(string)
		if strings.TrimSpace(p) == "" {
			continue
		}
		abs, err := r.locateFile(p)
		if err != nil {
			return nil, fmt.Errorf("attachment %s: %v", p, err)
		}
		// This is the one path that sends bytes off the machine, so it
		// follows the write rule (project only, unless allow_outside_project),
		// not the read rule.
		if _, err := r.files.Resolve(abs, true); err != nil {
			return nil, fmt.Errorf("attachment %s is outside the project directory; copy it into the project first", p)
		}
		st, err := os.Stat(abs)
		if err != nil {
			return nil, fmt.Errorf("attachment %s: %v", p, shortErr(err))
		}
		if !st.Mode().IsRegular() {
			return nil, fmt.Errorf("attachment %s is not a file", p)
		}
		if st.Size() == 0 {
			return nil, fmt.Errorf("attachment %s is empty", p)
		}
		if st.Size() > finalechat.MaxAttachmentSize {
			return nil, fmt.Errorf("attachment %s is %s; the limit is 10 MB", p, humanBytes(st.Size()))
		}
		att, err := r.copyAttachment(abs, "out")
		if err != nil {
			return nil, err
		}
		out = append(out, att)
	}
	return out, nil
}

// copyAttachment copies a file into the session's attachments directory
// and describes it.
func (r *Runtime) copyAttachment(src, prefix string) (event.Attachment, error) {
	dir := attachmentsDir(r.sess.Path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return event.Attachment{}, err
	}
	name := safeName(src)
	dst := filepath.Join(dir, fmt.Sprintf("%s-%d-%s", prefix, time.Now().UnixNano()%1_000_000_000, name))
	in, err := os.Open(src)
	if err != nil {
		return event.Attachment{}, err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return event.Attachment{}, err
	}
	head := make([]byte, 512)
	n, _ := io.ReadFull(in, head)
	head = head[:n]
	if _, err := out.Write(head); err != nil {
		out.Close()
		return event.Attachment{}, err
	}
	size, err := io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return event.Attachment{}, err
	}
	ct := contentTypeFor(name, head)
	return event.Attachment{Path: dst, Name: name, ContentType: ct, Size: size + int64(n), Kind: kindFor(ct)}, nil
}

// downloadAttachment saves a file the user sent from their phone.
func (r *Runtime) downloadAttachment(client *finalechat.Client, a finalechat.Attachment) (event.Attachment, error) {
	dir := attachmentsDir(r.sess.Path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return event.Attachment{}, err
	}
	short := a.ID
	if len(short) > 8 {
		short = short[:8]
	}
	name := safeName(a.Filename)
	dst := filepath.Join(dir, "in-"+short+"-"+name)
	f, err := os.Create(dst)
	if err != nil {
		return event.Attachment{}, err
	}
	ct, _, err := client.Download(r.ctx, a.URL, f)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(dst)
		return event.Attachment{}, err
	}
	st, err := os.Stat(dst)
	if err != nil {
		return event.Attachment{}, err
	}
	if st.Size() == 0 {
		_ = os.Remove(dst)
		return event.Attachment{}, errors.New("empty download")
	}
	if a.ContentType != "" {
		ct = a.ContentType
	}
	if i := strings.IndexByte(ct, ';'); i > 0 {
		ct = ct[:i]
	}
	kind := a.Kind
	if kind == "" {
		kind = kindFor(ct)
	}
	return event.Attachment{Path: dst, Name: name, ContentType: ct, Size: st.Size(), Kind: kind, Width: a.Width, Height: a.Height, ID: a.ID}, nil
}

// attachmentFiles loads attachments for upload; unreadable ones are skipped.
func attachmentFiles(atts []event.Attachment) []finalechat.File {
	var out []finalechat.File
	for _, a := range atts {
		raw, err := os.ReadFile(a.Path)
		if err != nil || len(raw) == 0 || len(raw) > finalechat.MaxAttachmentSize {
			continue
		}
		out = append(out, finalechat.File{Name: a.Name, ContentType: a.ContentType, Data: raw})
	}
	return out
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/float64(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/float64(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// attachmentSummary is a short human line, for the terminal.
func attachmentSummary(atts []event.Attachment) string {
	if len(atts) == 0 {
		return ""
	}
	var parts []string
	for _, a := range atts {
		parts = append(parts, fmt.Sprintf("%s (%s, %s)", a.Name, a.ContentType, humanBytes(a.Size)))
	}
	return "📎 " + strings.Join(parts, ", ")
}

// viewImage prepares a picture for a model to look at: the file must be an
// image of a supported type, and a copy goes into the session so the log
// can be replayed later.
func (r *Runtime) viewImage(p string) (event.Attachment, error) {
	abs, err := r.locateFile(p)
	if err != nil {
		return event.Attachment{}, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return event.Attachment{}, fmt.Errorf("%s: %v", p, shortErr(err))
	}
	if !st.Mode().IsRegular() || st.Size() == 0 {
		return event.Attachment{}, fmt.Errorf("%s is not a file with content", p)
	}
	if st.Size() > finalechat.MaxAttachmentSize {
		return event.Attachment{}, fmt.Errorf("%s is %s; images over 10 MB cannot be shown", p, humanBytes(st.Size()))
	}
	att, err := r.copyAttachment(abs, "view")
	if err != nil {
		return event.Attachment{}, err
	}
	if !att.IsImage() {
		_ = os.Remove(att.Path)
		return event.Attachment{}, fmt.Errorf("%s is %s, not an image; use read_file for text", p, att.ContentType)
	}
	if f, err := os.Open(att.Path); err == nil {
		if cfg, _, err := image.DecodeConfig(f); err == nil {
			att.Width, att.Height = cfg.Width, cfg.Height
		}
		f.Close()
	}
	return att, nil
}

// stashImage keeps a tool's picture until its result is recorded. Tool
// calls run concurrently across tasks, so this is keyed by call id.
func (r *Runtime) stashImage(callID string, att event.Attachment) {
	r.toolImagesMu.Lock()
	defer r.toolImagesMu.Unlock()
	if r.toolImages == nil {
		r.toolImages = map[string][]event.Attachment{}
	}
	r.toolImages[callID] = append(r.toolImages[callID], att)
}

func (r *Runtime) takeImages(callID string) []event.Attachment {
	r.toolImagesMu.Lock()
	defer r.toolImagesMu.Unlock()
	out := r.toolImages[callID]
	delete(r.toolImages, callID)
	return out
}

// locateFile finds a file the model named. Models copy long paths from the
// log imperfectly, so after the exact path (absolute or project-relative)
// it tries the file name alone: in the project, the session's attachments,
// and the project tree a few levels deep.
func (r *Runtime) locateFile(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", errors.New("path is required")
	}
	abs, err := r.files.Resolve(p, false)
	if err != nil {
		return "", err
	}
	if st, err := os.Stat(abs); err == nil && st.Mode().IsRegular() {
		return abs, nil
	}
	base := filepath.Base(p)
	for _, cand := range []string{filepath.Join(r.opts.Project, base), filepath.Join(attachmentsDir(r.sess.Path), base)} {
		if st, err := os.Stat(cand); err == nil && st.Mode().IsRegular() {
			return cand, nil
		}
	}
	if found := findByName(r.opts.Project, base, 4); found != "" {
		return found, nil
	}
	if matches, _ := filepath.Glob(filepath.Join(attachmentsDir(r.sess.Path), "*-"+base)); len(matches) > 0 {
		return matches[len(matches)-1], nil
	}
	return "", fmt.Errorf("no such file (looked for the exact path and for %q in the project); give the path exactly as it appears in the log, or just the file name", base)
}

// findByName walks a tree for a file with the given name, skipping the
// usual heavy directories, and returns the shallowest match.
func findByName(root, name string, maxDepth int) string {
	rootDepth := strings.Count(filepath.Clean(root), string(filepath.Separator))
	best := ""
	bestDepth := 1 << 30
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		depth := strings.Count(path, string(filepath.Separator)) - rootDepth
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", ".agents", "vendor", "target", "__pycache__", ".venv", "venv":
				if path != root {
					return filepath.SkipDir
				}
			}
			if depth >= maxDepth {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == name && depth < bestDepth {
			best, bestDepth = path, depth
		}
		return nil
	})
	return best
}
