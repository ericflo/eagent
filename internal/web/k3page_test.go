package web

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The Stop button shows why a stop was refused (a session that is ending,
// say, answered 409). It used to leave the refusal unshown, and the user
// saw nothing happen.
func TestK3TheStopButtonShowsARefusal(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	src, err := os.ReadFile(filepath.Join("static", "app.js"))
	if err != nil {
		t.Fatal(err)
	}
	tools := regexp.MustCompile(`(?s)function sessionTools\(d\) \{.*?\n\}\n`).FindString(string(src))
	if tools == "" {
		t.Fatal("app.js has no sessionTools()")
	}
	script := `
const made = [];
const h = (tag, attrs, ...kids) => { const n = {tag, attrs: attrs || {}, kids}; made.push(n); return n; };
const ARCHIVE = null;
const S = {sid: 's1', detail: {alive: true}};
const toolBadges = () => [];
const confirm = () => true;
const told = [];
const toast = (m, kind) => { told.push([m, kind]); };
const api = async () => { const e = new Error('the session is stopping and takes nothing more'); e.status = 409; throw e; };
` + tools + `
sessionTools({alive: true});
const button = made.find(n => n.attrs.id === 'session-act');
Promise.resolve(button.attrs.onclick()).catch(() => {}).then(() => {
  if (!told.some(([m]) => m === 'the session is stopping and takes nothing more')) { console.error('shown: ' + JSON.stringify(told)); process.exit(1); }
});
`
	if out, err := exec.Command("node", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, strings.TrimSpace(string(out)))
	}
}
