package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/config"
)

// `eagent doctor` says where the conversation restarts and how the number
// was chosen. It said "half of the model's context window" for every
// window; on a small one (a local 32k model with an 8k reply ceiling) the
// number is not half of it (config.AutoRolloverWithin holds it elsewhere),
// so the line was false. It says half only when the number is half, and
// names the small-window rule otherwise.
func TestSR9DoctorRolloverLineSaysHalfOnlyWhenItIsHalf(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("EAGENT_FINALECHAT", "")
	for _, c := range []struct {
		window   int
		wantHalf bool
	}{{32768, false}, {200_000, true}} {
		project := t.TempDir()
		if err := os.MkdirAll(filepath.Dir(config.File(project)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(config.File(project), []byte(`{"model":{"context_tokens":`+strconv.Itoa(c.window)+`,"max_tokens":8192},"finalechat":{"enabled":false}}`), 0o644); err != nil {
			t.Fatal(err)
		}
		out := captureStdout(t, func() { cmdDoctor(project, "", "", false) })
		m := regexp.MustCompile(`conversation restarts at: (\d+) prompt tokens, (.*)`).FindStringSubmatch(out)
		if m == nil {
			t.Fatalf("no rollover line:\n%s", out)
		}
		n, _ := strconv.Atoi(m[1])
		isHalf := n >= c.window/2-1000 && n <= c.window/2+1000
		saysHalf := strings.HasPrefix(m[2], "half of the model's context window")
		if isHalf != c.wantHalf || saysHalf != isHalf || !saysHalf && !strings.Contains(m[2], "small-window rule") {
			t.Fatalf("on a %d-token window (half is %d) doctor says: conversation restarts at: %d prompt tokens, %s", c.window, c.window/2, n, m[2])
		}
	}
}
