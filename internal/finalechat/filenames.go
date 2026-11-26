package finalechat

import (
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	unsafeFileName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)
	unsafeFileID   = regexp.MustCompile(`[^A-Za-z0-9-]+`)
)

// maxNameBytes bounds a file's name once saved, so "in-" and a whole id in
// front of it stay well inside the 255 bytes a file system takes.
const maxNameBytes = 120

// SafeFileName reduces a file's name to something a shell and a browser
// both like: no directory, nothing but letters, digits, dots, dashes and
// underscores, at most 120 bytes. A long name keeps its start and its
// extension, so the file still opens as what it is.
func SafeFileName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	name = strings.Trim(unsafeFileName.ReplaceAllString(name, "_"), "._")
	if name == "" {
		return "file"
	}
	if len(name) <= maxNameBytes {
		return name
	}
	ext := filepath.Ext(name)
	if len(ext) > 20 {
		ext = ""
	}
	return clipBytes(strings.TrimSuffix(name, ext), maxNameBytes-len(ext)) + ext
}

// InboundFileName is the name a file the user sent from the phone is saved
// under: "in-", the server's whole id (its ids are UUIDv7, whose first
// characters are a clock shared by everything sent within a minute or
// so), and the file's own name made safe. It also returns that name.
// Neither the id nor the name can bring a "/" or ".." into the path.
func InboundFileName(id, filename string) (file, name string) {
	id = clipBytes(unsafeFileID.ReplaceAllString(id, "_"), 64)
	name = SafeFileName(filename)
	return "in-" + id + "-" + name, name
}

// clipBytes cuts s to at most n bytes on a character boundary.
func clipBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
