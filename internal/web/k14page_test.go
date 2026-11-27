package web

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// k14Send sends words from the composer with api answering answer, then
// runs after (JavaScript, given the page's functions), and returns what it
// prints.
func k14Send(t *testing.T, answer, after string) string {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	src, err := os.ReadFile(filepath.Join("static", "app.js"))
	if err != nil {
		t.Fatal(err)
	}
	var funcs []string
	for _, name := range []string{"send", "showQueued", "queuedNode", "showsWords", "takeQueued"} {
		f := regexp.MustCompile(`(?s)\nfunction ` + name + `\(.*?\n\}\n|\nasync function ` + name + `\(.*?\n\}\n`).FindString(string(src))
		if name == "send" && f == "" {
			t.Fatal("app.js has no send()")
		}
		funcs = append(funcs, f)
	}
	script := `
const all = [];
const node = (tag, attrs, kids) => { const n = {tag, attrs: attrs || {}, kids, removed: false, remove() { this.removed = true; }}; all.push(n); return n; };
const h = (tag, attrs, ...kids) => node(tag, attrs, kids.flat());
const text = n => !n ? '' : typeof n === 'string' ? n : (n.attrs.html || '') + (n.kids || []).map(text).join(' ');
const col = node('div', {class: 'col'}, []);
col.append = n => col.kids.push(n);
col.insertBefore = (n, ref) => col.kids.splice(col.kids.indexOf(ref), 0, n);
const working = node('div', {id: 'working'}, []);
col.kids.push(working);
const ta = {value: 'and add the retry test', style: {}};
const $ = sel => sel === '#input' ? ta : sel === '.chat .col' ? col : sel === '#working' ? working : sel === '.chat .scroll' ? {scrollHeight: 0} : null;
const document = {querySelector: sel => all.find(n => !n.removed && sel === '[data-queued="' + n.attrs['data-queued'] + '"]') || null};
const md = s => s;
const ARCHIVE = null;
const S = {sid: 's1', tab: 'chat', detail: {alive: true, agents: []}, events: [], lastSeq: 4, queued: [], queuedSeq: 0};
const clientCaps = () => ({});
const showScreen = () => {};
const told = [];
const toast = m => told.push(m);
const api = async () => (` + answer + `);
` + strings.Join(funcs, "\n") + `
const shown = () => col.kids.filter(n => !n.removed && n !== working).map(text).join(' | ');
Promise.resolve(send()).then(() => { ` + after + ` });
`
	out, err := exec.Command("node", "-e", script).CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out))
}

// Words the server answers "queued" (another process runs the session;
// they wait in its inbox, not yet on its record) show in the chat with
// the answer's line under them, until the record shows them. The page
// used to drop the answer: the words left the composer and showed
// nowhere.
func TestK14TheChatShowsWordsThatWaitWithTheAnswersLine(t *testing.T) {
	answer := `{delivered: 'queued', inbox: 'i1', message: 'They wait in the session\'s inbox until it next runs.'}`
	got := k14Send(t, answer, `console.log(shown());`)
	if !strings.Contains(got, "and add the retry test") || !strings.Contains(got, "They wait in the session's inbox until it next runs.") {
		t.Fatalf("the chat shows %q for words that wait", got)
	}
	// The record then shows them (a run took them): the waiting copy goes.
	got = k14Send(t, answer, `takeQueued({seq: 5, type: 'user.message', data: {text: 'and add the retry test', inbox: 'i0'}}); takeQueued({seq: 6, type: 'user.message', data: {text: 'and add the retry test', inbox: 'i1'}}); console.log(JSON.stringify([shown(), S.queued.length]));`)
	if got != `["",0]` {
		t.Fatalf("once the record shows the words, the chat still shows the waiting copy: %s", got)
	}
	// Words a run took at once show only from the record.
	if got := k14Send(t, `{delivered: 'inbox'}`, `console.log(JSON.stringify([shown(), S.queued.length]));`); got != `["",0]` {
		t.Fatalf("words delivered show a waiting copy: %s", got)
	}
}
