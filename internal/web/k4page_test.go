package web

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// k4Stop clicks the Stop button with api's body being answer (it returns
// the server's answer, or throws its refusal), and returns what was shown.
func k4Stop(t *testing.T, answer string) string {
	t.Helper()
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
const toast = (m, kind) => { told.push(m); };
const api = async () => { ` + answer + ` };
` + tools + `
sessionTools({alive: true});
const button = made.find(n => n.attrs.id === 'session-act');
Promise.resolve(button.attrs.onclick()).catch(() => {}).then(() => { console.log(JSON.stringify(told)); });
`
	out, err := exec.Command("node", "-e", script).CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out))
}

// A stop the server refuses (it is shutting down) shows the refusal; a
// plain stop shows nothing new.
func TestTheStopButtonShowsARefusal(t *testing.T) {
	if got := k4Stop(t, `throw new Error('eagent is shutting down: the session was not stopped')`); got != `["eagent is shutting down: the session was not stopped"]` {
		t.Fatalf("shown %s, want the refusal", got)
	}
	if got := k4Stop(t, `return {ok: true}`); got != `[]` {
		t.Fatalf("a plain stop showed %s", got)
	}
}
