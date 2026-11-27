package web

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// k2Page runs script after the page's send() in node, with what send()
// reads stubbed; script ends the process with an error when a check fails.
func k2Page(t *testing.T, script string) {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	src, err := os.ReadFile(filepath.Join("static", "app.js"))
	if err != nil {
		t.Fatal(err)
	}
	send := regexp.MustCompile(`(?s)async function send\(\) \{.*?\n\}\n`).FindString(string(src))
	if send == "" {
		t.Fatal("app.js has no send()")
	}
	if out, err := exec.Command("node", "-e", send+script).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, strings.TrimSpace(string(out)))
	}
}

// k2Stubs stand in for the page around send(): the box holds box, and the
// session's detail is detail.
func k2Stubs(box, detail string) string {
	return `
const ta = {value: ` + box + `, style: {}};
const $ = () => ta;
const S = {sid: 's1', detail: ` + detail + `};
const clientCaps = () => ({});
const sent = [], screens = [];
const toast = () => {};
const api = async (path) => { sent.push(path); return {}; };
const showScreen = (name) => { screens.push(name); };
`
}

// Finding 15 of the fifteenth review: /screen typed in the web composer
// shows the agent's terminal there, as the Screen button does; it used to
// be sent, and the session showed the screen only at eagent's own
// terminal and on the phone, so the page showed nothing.
func TestK2ScreenFromTheWebShowsTheAgentsTerminal(t *testing.T) {
	k2Page(t, k2Stubs(`'/screen'`, `{alive: true, agents: [{name: 'old', state: 'stopped'}, {name: 'codex', state: 'idle'}]}`)+`
send().then(() => {
  if (screens.join() !== 'codex' || sent.length !== 0 || ta.value !== '') { console.error('screens ' + JSON.stringify(screens) + ', sent ' + JSON.stringify(sent) + ', box ' + JSON.stringify(ta.value)); process.exit(1); }
});
`)
}

// With no agent running it is sent as before: the session says so in the
// conversation.
func TestK2ScreenWithNoAgentRunningIsSent(t *testing.T) {
	k2Page(t, k2Stubs(`'/screen'`, `{alive: true, agents: [{name: 'old', state: 'stopped'}]}`)+`
send().then(() => {
  if (screens.length !== 0 || sent.join() !== '/api/sessions/s1/message') { console.error('screens ' + JSON.stringify(screens) + ', sent ' + JSON.stringify(sent)); process.exit(1); }
});
`)
}
