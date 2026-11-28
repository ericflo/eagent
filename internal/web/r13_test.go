package web

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/ericflo/eagent/internal/config"
)

// The settings page's automatic rollover is the one Config.Rollover gives
// (config.AutoRolloverWithin with no system prompt known): on a small
// window it is held inside what the route can take as input, not raised to
// 20,000. config.js copies the Go rule, so the two are run side by side.
func TestThePagesAutomaticRolloverIsConfigsRollover(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	src, err := os.ReadFile(filepath.Join("static", "config.js"))
	if err != nil {
		t.Fatal(err)
	}
	fn := regexp.MustCompile(`(?s)function autoRollover\(win, maxTok\) \{.*?\n\}\n`).FindString(string(src))
	if fn == "" {
		t.Fatal("config.js has no autoRollover()")
	}
	cases := [][2]int{{32768, 0}, {32768, 16384}, {48000, 32768}, {16384, 2048}, {200000, 0}, {200000, 32768}, {1048576, 32768}, {0, 0}, {65536, 32768}, {96000, 32768}}
	in, _ := json.Marshal(cases)
	out, err := exec.Command("node", "-e", fn+"\nconsole.log(JSON.stringify("+string(in)+".map(([w, m]) => autoRollover(w, m))));").CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var got []int
	if err := json.Unmarshal(out, &got); err != nil || len(got) != len(cases) {
		t.Fatalf("node printed %s (%v)", out, err)
	}
	for i, c := range cases {
		cfg := config.Config{Model: config.ModelRoute{ContextTokens: c[0], MaxTokens: c[1]}}
		if want := cfg.Rollover(); got[i] != want {
			t.Errorf("config.js autoRollover(%d, %d) = %d, Config.Rollover() = %d", c[0], c[1], got[i], want)
		}
	}
}
