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
	"github.com/ericflo/eagent/internal/relay"
)

// Attachments are files that travel with messages: screenshots the user
// sends from their phone, and files eagent sends back. Every one is
// copied under <session>/attachments/ so the log points at bytes that will
// still be there on replay, whatever happens to the project directory.

// attachmentsDir is where a session keeps message files.
func attachmentsDir(sessionPath string) string { return filepath.Join(sessionPath, "attachments") }

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// unsafeID is what may not stay of a server's attachment id in a file name.
var unsafeID = regexp.MustCompile(`[^A-Za-z0-9-]+`)

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

// messageAttachments validates the paths eagent wants to send with a
// message and copies each into the session so the log stays self-contained.
// Errors are written for the model to read.
func (r *Runtime) messageAttachments(raw any) ([]event.Attachment, error) {
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
		// This is the one path that sends bytes off the machine, so the file
		// must really be where the session works, not merely named as if it
		// were: symlinks are followed before the check.
		if real, err := filepath.EvalSymlinks(abs); err == nil {
			abs = real
		}
		if !r.sendable(abs) {
			return nil, fmt.Errorf("attachment %s is outside the project and the agent's folder, and the agent did not report it as a picture it made", p)
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
	// Both come from the server: neither may bring a "/" or ".." into the
	// path. An id is a uuid; anything but letters, digits and dashes goes.
	short := unsafeID.ReplaceAllString(a.ID, "_")
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

// viewImage prepares a picture for eagent's model to look at: the file must
// be an image of a supported type, and a copy goes into the session so the
// conversation renders the same bytes every time, whatever happens to the
// original. Not on the loop goroutine.
// imageStamp identifies the file a path names by where it is, its size
// and its time, or "" when it cannot be read.
func (r *Runtime) imageStamp(p string) string {
	abs, err := r.locateFile(p)
	if err != nil {
		return ""
	}
	st, err := os.Stat(abs)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%s|%d|%d", abs, st.Size(), st.ModTime().UnixNano())
}

func (r *Runtime) viewImage(p string) (event.Attachment, error) {
	abs, err := r.locateFile(p)
	if err != nil {
		return event.Attachment{}, err
	}
	// Looking at a picture sends its bytes to the model's provider and
	// keeps a copy in the session, from where it could be attached to a
	// message. eagent has no other way to read a file, so this is held to
	// the rule a sent file is: the picture is where the session works, an
	// agent reported it, or the user named it. Symlinks are followed
	// before the check.
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	if !r.sendable(abs) && !r.named(abs) {
		return event.Attachment{}, fmt.Errorf("%s is outside the project and the agent's folder, and neither the agent nor the user named it. Ask the user for the path if they want you to look at it", p)
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
	switch att.ContentType {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
	default:
		_ = os.Remove(att.Path)
		return event.Attachment{}, fmt.Errorf("%s is %s, not an image you can look at (png, jpeg, gif, webp)", p, att.ContentType)
	}
	if f, err := os.Open(att.Path); err == nil {
		if cfg, _, err := image.DecodeConfig(f); err == nil {
			att.Width, att.Height = cfg.Width, cfg.Height
		}
		f.Close()
	}
	return att, nil
}

// sessionRoots are the folders the session works in: the project and each
// running agent's folder. Not on the loop goroutine.
func (r *Runtime) sessionRoots() []string {
	roots := []string{r.projectPath()}
	r.sync(func() {
		if wd := r.workDir(); wd != roots[0] {
			roots = append(roots, wd)
		}
		for _, rel := range r.st.ActiveRelays() {
			roots = append(roots, rel.Dir)
		}
	})
	return roots
}

// locateFile finds a file the model named. Models copy long paths from the
// record imperfectly, so after the exact path (absolute, or relative to the
// project or an agent's folder) it tries the file name alone: in those
// folders, the session's attachments, and their trees a few levels deep.
func (r *Runtime) locateFile(p string) (string, error) {
	p = relay.ExpandHome(strings.TrimSpace(p))
	if p == "" {
		return "", errors.New("path is required")
	}
	regular := func(path string) bool {
		st, err := os.Stat(path)
		return err == nil && st.Mode().IsRegular()
	}
	roots := r.sessionRoots()
	if filepath.IsAbs(p) {
		if regular(p) {
			return filepath.Clean(p), nil
		}
	} else {
		for _, root := range roots {
			if cand := filepath.Join(root, p); regular(cand) {
				return cand, nil
			}
		}
	}
	base := filepath.Base(p)
	for _, root := range append(roots, attachmentsDir(r.sess.Path)) {
		if cand := filepath.Join(root, base); regular(cand) {
			return cand, nil
		}
	}
	for _, root := range roots {
		if found := findByName(root, base, 4); found != "" {
			return found, nil
		}
	}
	if matches, _ := filepath.Glob(filepath.Join(attachmentsDir(r.sess.Path), "*-"+base)); len(matches) > 0 {
		return matches[len(matches)-1], nil
	}
	return "", fmt.Errorf("no such file (looked for the exact path and for %q where the session works); give the path exactly as it appears in your conversation, or just the file name", base)
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

// named reports whether the user or an agent wrote this path (as an
// absolute path or one under ~) in a message, an answer, a progress line or
// a turn report. The user's word is leave to look at a file wherever it
// is, and an agent that names a picture mid-turn (a screenshot it saved
// under /tmp, say) has not finished the turn that would list it. Not on
// the loop goroutine.
func (r *Runtime) named(abs string) bool {
	var texts []string
	r.sync(func() {
		for _, ev := range r.st.Events {
			switch ev.Type {
			case event.UserMessage, event.UserAnswer, event.RelayProgress, event.RelayTurn:
			default:
				continue
			}
			var d struct {
				Text string `json:"text"`
			}
			if ev.Decode(&d) == nil && strings.ContainsAny(d.Text, "/~") {
				texts = append(texts, d.Text)
			}
		}
	})
	// A path with a space in it is not one word: it counts when the text
	// holds it whole, as written or under ~, with nothing of a longer
	// path on either side.
	whole := []string{abs}
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(abs, home+string(filepath.Separator)) {
		whole = append(whole, "~"+strings.TrimPrefix(abs, home))
	}
	pathByte := func(c byte) bool {
		return c == '/' || c == '_' || c == '-' || c == '.' || c == '~' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80
	}
	for _, text := range texts {
		for _, w := range whole {
			if !strings.Contains(w, " ") {
				continue // the words below cover it
			}
			for from := 0; ; {
				i := strings.Index(text[from:], w)
				if i < 0 {
					break
				}
				i += from
				end := i + len(w)
				before := i == 0 || !pathByte(text[i-1])
				after := end == len(text) || !pathByte(text[end]) || text[end] == '.' && (end+1 == len(text) || !pathByte(text[end+1]))
				if before && after {
					return true
				}
				from = i + 1
			}
		}
		for _, word := range strings.Fields(text) {
			word = strings.Trim(word, "\"'`()[]<>,;:!?")
			if !strings.HasPrefix(word, "/") && !strings.HasPrefix(word, "~") {
				continue
			}
			for _, cand := range []string{word, strings.TrimRight(word, ".")} {
				path := filepath.Clean(relay.ExpandHome(cand))
				if real, err := filepath.EvalSymlinks(path); err == nil {
					path = real
				}
				if path == abs {
					return true
				}
			}
		}
	}
	return false
}

// sendable reports whether an already-resolved absolute path may be sent
// to the user: it lies where the session works (the project, an agent's
// folder, the session's own files), or an agent's transcript named it as a
// picture it made or looked at. Not on the loop goroutine.
func (r *Runtime) sendable(abs string) bool {
	roots := append(r.sessionRoots(), r.sess.Path)
	for _, root := range roots {
		root = filepath.Clean(root)
		if real, err := filepath.EvalSymlinks(root); err == nil {
			root = real
		}
		if abs == root || strings.HasPrefix(abs, root+string(filepath.Separator)) {
			return true
		}
	}
	reported := false
	r.sync(func() {
		for i := len(r.st.Events) - 1; i >= 0 && !reported; i-- {
			ev := r.st.Events[i]
			if ev.Type != event.RelayTurn {
				continue
			}
			var d event.RelayTurnData
			if ev.Decode(&d) != nil {
				continue
			}
			for _, img := range d.Images {
				// Agents also write a picture's path under ~ or from
				// their own folder.
				img = relay.ExpandHome(img)
				if rel := r.st.RelayAt(d.Name, ev.Seq); !filepath.IsAbs(img) && rel != nil && rel.Dir != "" {
					img = filepath.Join(rel.Dir, img)
				}
				if real, err := filepath.EvalSymlinks(img); err == nil {
					img = real
				}
				if filepath.Clean(img) == abs {
					reported = true
					break
				}
			}
		}
	})
	return reported
}
