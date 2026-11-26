package finalechat

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// A name in any script keeps its extension, with its dot; a name that is
// only an extension keeps it; a cut never splits a character.
func TestSafeFileNameKeepsTheExtensionInEveryScript(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"日本語.pdf", "file.pdf"},
		{"отчёт.docx", "file.docx"},
		{"صورة.png", "file.png"},
		{"résumé.pdf", "r_sum.pdf"},
		{"📷 IMG 1.jpg", "IMG_1.jpg"},
		{".pdf", "file.pdf"},
		{"pdf", "pdf"},
		{"report.tar.gz", "report.tar.gz"},
		{"  spaced name .txt ", "spaced_name.txt"},
		{"../../etc/passwd", "passwd"},
		{"日本語", "file"},
		{"", "file"},
	} {
		if got := SafeFileName(c.in); got != c.want {
			t.Errorf("SafeFileName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// Combining characters at the cut: the name is cut to 120 bytes and
	// stays valid, its extension kept.
	long := strings.Repeat("é", 100) + ".png"
	if got := SafeFileName(long); len(got) > 120 || !strings.HasSuffix(got, ".png") || !utf8.ValidString(got) {
		t.Errorf("SafeFileName(long combining) = %q (%d bytes)", got, len(got))
	}
	if got := clipBytes(strings.Repeat("é", 10), 4); !utf8.ValidString(got) || len(got) > 4 {
		t.Errorf("clipBytes cut a character: %q", got)
	}
}
