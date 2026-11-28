package web

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// r16Page runs script after the named functions of app.js, with a page of
// plain objects in place of the document, and returns what it prints.
func r16Page(t *testing.T, names []string, script string) string {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	src, err := os.ReadFile(filepath.Join("static", "app.js"))
	if err != nil {
		t.Fatal(err)
	}
	var funcs []string
	for _, name := range names {
		f := regexp.MustCompile(`(?s)\n(async )?function ` + name + `\(.*?\n\}\n`).FindString(string(src))
		if f == "" {
			t.Fatalf("app.js has no %s()", name)
		}
		funcs = append(funcs, f)
	}
	pre := `
const text = n => n === null || n === undefined || n === false ? '' : typeof n !== 'object' ? String(n) : (n.kids || []).map(text).join('');
const h = (tag, attrs, ...kids) => { const n = {tag, attrs: attrs || {}, kids: kids.flat()}; Object.defineProperty(n, 'textContent', {get() { return text(n); }, set(v) { n.kids = [v]; }}); return n; };
const ARCHIVE = null;
const clip = (s, n) => String(s || '');
`
	out, err := exec.Command("node", "-e", pre+strings.Join(funcs, "\n")+script).CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out))
}

// A message of the user's was on its way to Claude when a switch handed
// the work to Codex, and it was passed on (relay.moved). Its receipt is
// under Codex's name: the chat shows it, a failure too. The page looked
// for a send under the receipt's own name, found none and showed nothing.
func TestTheChatShowsTheReceiptOfAMessagePassedToAnotherAgent(t *testing.T) {
	got := r16Page(t, []string{"chatNode", "agentWho"}, `
const S = {detail: {agents: [{name: 'claude', who: 'Claude'}, {name: 'codex', who: 'Codex'}]}, events: [
  {type: 'relay.send', data: {name: 'claude', id: 'r1', from: 'user', text: 'run the tests'}},
  {type: 'relay.moved', data: {name: 'codex', id: 'r1', from: 'user', text: 'run the tests', prev: 'claude'}},
  {type: 'relay.send', data: {name: 'codex', id: 'r2', from: 'eagent', text: 'keep going'}},
]};
const show = d => text(chatNode({type: 'relay.delivered', data: d}));
console.log(JSON.stringify([show({name: 'codex', id: 'r1', ok: true}), show({name: 'codex', id: 'r1', ok: false, outcome: 'not_sent', detail: 'x'}), show({name: 'codex', id: 'r2', ok: true})]));
`)
	if got != `["delivered to Codex","did not reach Codex: x",""]` {
		t.Fatalf("the chat shows %s", got)
	}
}

// The Stop button of a running session is still on the page while a
// click on another, stopped session loads that one. A click on it then
// asked nothing and resumed the other session ("resuming"). The button is
// its own session's: it asks first and stops that one.
func TestTheStopButtonStopsItsOwnSessionWhileAnotherLoads(t *testing.T) {
	got := r16Page(t, []string{"sessionTools"}, `
const S = {sid: 's1', detail: {alive: true}};
const toolBadges = () => [];
let asked = 0;
const confirm = () => { asked++; return true; };
const told = [], calls = [];
const toast = m => told.push(m);
const api = async path => { calls.push(path); return {}; };
const tools = sessionTools({alive: true});
const button = tools.kids.find(n => n && n.attrs && n.attrs.id === 'session-act');
S.sid = 's2'; S.detail = null; // the click on the other session: its detail is still loading
Promise.resolve(button.attrs.onclick()).then(() => console.log(JSON.stringify({label: button.textContent, asked, calls, told})));
`)
	if got != `{"label":"Stop","asked":1,"calls":["/api/sessions/s1/stop"],"told":[]}` {
		t.Fatalf("a click on Stop while another session loads: %s", got)
	}
}
