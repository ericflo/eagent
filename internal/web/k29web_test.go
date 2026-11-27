package web

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// k29Refused is the page around send() while the server holds the request
// and then refuses it (503 during a shutdown): cur is the chat box on the
// page, which the script may replace or write in before it calls refuse().
const k29Refused = `
let cur = {value: 'and add the retry test', style: {}};
const $ = () => cur;
const S = {sid: 's1', detail: {alive: true, agents: []}, drafts: {}};
const clientCaps = () => ({});
const told = [];
const toast = m => told.push(m);
const showScreen = () => {};
const showQueued = () => {};
let reject;
const api = () => new Promise((_, rej) => { reject = rej; });
const refuse = () => { const e = new Error('eagent is stopping; send it again once it is back'); e.status = 503; reject(e); };
const want = (what, got, exp) => { if (got !== exp) { console.error(what + ': ' + JSON.stringify(got) + ', want ' + JSON.stringify(exp) + '; told ' + JSON.stringify(told)); process.exit(1); } };
const p = send();
`

// Words the server refuses go back where the user can send them again. A
// refusal can take seconds (the server waits for a run that is ending),
// and the chat box may be rebuilt meanwhile (the Timeline tab and back):
// the words are put into the box that is on the page now.
func TestK29RefusedWordsGoIntoTheBoxOnThePageNow(t *testing.T) {
	k2Page(t, k29Refused+`
cur = {value: '', style: {}};
refuse();
p.then(() => { want('the box on the page', cur.value, 'and add the retry test'); want('told', told.length, 1); });
`)
}

// The user wrote more in the box while the request waited: the refused
// words go in front of it, and nothing is dropped.
func TestK29RefusedWordsGoInFrontOfWhatWasTypedSince(t *testing.T) {
	k2Page(t, k29Refused+`
cur.value = 'then push'; S.drafts.s1 = 'then push'; // as the box's oninput keeps it
refuse();
p.then(() => want('the box', cur.value, 'and add the retry test\nthen push'));
`)
}

// The user opened another session meanwhile: its box is not written in,
// and the words are kept as the draft of the session they were for.
func TestK29RefusedWordsAreKeptForTheirOwnSession(t *testing.T) {
	k2Page(t, k29Refused+`
S.sid = 's2'; cur = {value: 'for the other session', style: {}};
refuse();
p.then(() => { want('the other session\'s box', cur.value, 'for the other session'); want('the draft kept', S.drafts.s1, 'and add the retry test'); });
`)
}

// The chat box is built anew whenever the chat is shown. It is filled
// from the session's draft: what was typed and not sent, or sent and
// refused while the box was off the page.
func TestK29TheChatBoxIsFilledFromTheSessionsDraft(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	src, err := os.ReadFile(filepath.Join("static", "app.js"))
	if err != nil {
		t.Fatal(err)
	}
	render := regexp.MustCompile(`(?s)function renderChat\(pane\) \{.*?\n\}\n`).FindString(string(src))
	if render == "" {
		t.Fatal("app.js has no renderChat()")
	}
	script := `
let made = [];
const h = (tag, attrs, ...kids) => { const n = {tag, attrs: attrs || {}, kids, value: '', style: {}, scrollHeight: 20, append() {}}; made.push(n); return n; };
const $ = sel => made.findLast(n => '#' + n.attrs.id === sel);
const ARCHIVE = null;
const S = {sid: 's1', tab: 'chat', detail: {alive: true}, events: [], queued: [], drafts: {}};
const chatNode = () => null, queuedNode = () => null, showsWords = () => false;
const updateComposer = () => {}, renderLive = () => {}, activityShown = () => false, setActivity = () => {}, send = () => {};
const pane = {classList: {add() {}}, style: {}, replaceChildren() {}};
` + render + `
const want = (what, got, exp) => { if (got !== exp) { console.error(what + ': ' + JSON.stringify(got) + ', want ' + JSON.stringify(exp)); process.exit(1); } };
renderChat(pane);
const box = $('#input');
want('a new session\'s box', box.value, '');
box.value = 'half a thought'; box.attrs.oninput({target: box});
want('the draft kept as it is typed', S.drafts.s1, 'half a thought');
made = []; renderChat(pane); // the Timeline tab and back
want('the box after it was rebuilt', $('#input').value, 'half a thought');
S.sid = 's2'; made = []; renderChat(pane);
want('another session\'s box', $('#input').value, '');
`
	if out, err := exec.Command("node", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, strings.TrimSpace(string(out)))
	}
}
