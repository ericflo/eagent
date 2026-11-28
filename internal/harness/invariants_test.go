package harness

// Two checks that run over every session any test in this package runs. No
// test asks for them: TestMain turns them on and fails the run on what they
// find.
//
//  1. When a session's loop ends, its state in memory (turnState) is compared
//     with the same state rebuilt from the record, the way a resume rebuilds
//     it. A field that is written live and not rebuilt the same is a session
//     that behaves differently after a restart of eagent.
//  2. Each request to the model must only add to the one before it in its
//     conversation: the same tool list, the same options, the earlier
//     messages byte for byte. Where the loop rewrites the end of a
//     conversation on purpose, the rule that allows it is named below.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/store"
)

var inv struct {
	mu       sync.Mutex
	servers  []invServer
	bad      map[string]bool
	sessions int
	requests int
}

type invServer struct {
	test string
	s    *scripted
}

func invFail(format string, args ...any) {
	inv.mu.Lock()
	defer inv.mu.Unlock()
	if inv.bad == nil {
		inv.bad = map[string]bool{}
	}
	inv.bad[fmt.Sprintf(format, args...)] = true
}

// invReport is what the two checks found, sorted, and how much they covered.
func invReport() (found []string, sessions, requests int) {
	invRequests()
	inv.mu.Lock()
	defer inv.mu.Unlock()
	for s := range inv.bad {
		found = append(found, s)
	}
	sort.Strings(found)
	return found, inv.sessions, inv.requests
}

// ---- 1: the loop's state against the record --------------------------------

// invRebuiltOtherwise lists the fields of turnState a resume does not rebuild
// to the value the loop held, each with what that means for a session resumed
// after a clean stop. Every other field must agree; a field added to
// turnState is compared unless it is added here with its reason.
var invRebuiltOtherwise = map[string]string{
	"tickGen":         "the ticker's generation, set by Run",
	"finalAsked":      "this run asked for its closing word; a resumed run is a new run",
	"rereadTold":      "reset at the start of every dossier pass and read only during one",
	"futileLogged":    "only the terminal line repeats; the decision reads the notice in the record",
	"lookedAt":        "pacing of progress looks starts again",
	"lookSteps":       "pacing of progress looks starts again",
	"failures":        "the back-off after failed calls is not recorded: a resumed session tries at once",
	"retryAt":         "as failures",
	"failSeq":         "as failures",
	"failTold":        "as failures: after two more failed calls the user is told again",
	"heldCut":         "as failures: a message a failed call cut off unanswered is taken up by the retry of the same run",
	"overflowed":      "a provider's refusal for size is not recorded: a resumed session sends the refused request once more before it restarts",
	"promptTokens":    "as overflowed: the size the refusal raised is not recorded",
	"dossierFailedAt": "the back-off after a dossier that could not be written; a new process tries at once",
}

// invWritesState names tests that set turnState fields by hand, so their
// sessions hold state no record could explain.
var invWritesState = map[string]string{
	"TestTailKeepsTheNarratorsCadence":                  "sets saidAt directly",
	"TestSendMessageAttachesAPictureThatViewImageTakes": "runs send_message by hand, outside any model call, so no reply records what it had seen",
}

var invTmpRe = regexp.MustCompile(`^(Test[^/]*?)\d+$`)

// invTestOf reads a test's name from the t.TempDir path of its project.
func invTestOf(project string) string {
	for _, el := range strings.Split(filepath.ToSlash(project), "/") {
		if m := invTmpRe.FindStringSubmatch(el); m != nil {
			return m[1]
		}
	}
	return "a test outside t.TempDir (" + project + ")"
}

// invAfterRun runs on the loop's goroutine once a session has finished.
func invAfterRun(r *Runtime) {
	live := r.turns
	if live.busy || live.finalBusy || live.compacting {
		return // stopped with a call still out: the record is behind on purpose
	}
	if r.recordFailed {
		// An event could not be written, and the session ended for it: what
		// the loop did between that write and its end (a model call that
		// came back) is in its state and cannot be on the record. A resume
		// starts from the record.
		return
	}
	test := invTestOf(r.opts.Project)
	if _, ok := invWritesState[test]; ok {
		return
	}
	events, err := store.Read(r.sess.Path)
	if err != nil {
		return // the test took the record away
	}
	st := state.Replay(events)
	st.SetPrompts(r.prompts)
	var fresh turnState
	fresh.init(st)
	inv.mu.Lock()
	inv.sessions++
	inv.mu.Unlock()
	for _, d := range invCompare(&live, &fresh) {
		invFail("%s: after the session ended, %s is %s in the loop and %s rebuilt from the record", test, d[0], d[1], d[2])
	}
	// A send can wait to be confirmed only while its agent is there to
	// confirm it. (A message of the user's that a switch left to its brief
	// keeps no outcome of its own: briefedSends.)
	for name, rel := range st.Relays {
		if n := len(rel.Undelivered) - len(briefedSends(st, rel)); (rel.Ended || rel.State == "exited") && n > 0 {
			invFail("%s: %d sends are still waiting on %s, which is %s", test, n, name, rel.State)
		}
	}
}

var (
	invTimerT = reflect.TypeOf((*time.Timer)(nil))
	invTimeT  = reflect.TypeOf(time.Time{})
)

// invCompare is every field that differs, as {name, live, rebuilt}.
func invCompare(a, b *turnState) [][3]string {
	var out [][3]string
	va, vb := reflect.ValueOf(a).Elem(), reflect.ValueOf(b).Elem()
	ty := va.Type()
	for i := 0; i < ty.NumField(); i++ {
		f := ty.Field(i)
		if f.Type == invTimerT {
			continue // armed by Run
		}
		if _, ok := invRebuiltOtherwise[f.Name]; ok {
			continue
		}
		fa, fb := va.Field(i), vb.Field(i)
		if f.Name == "wakes" {
			// A wake no newer than what was seen asks for nothing, so only
			// the ones still owed a look are compared.
			var oa, ob [wakeKinds]int64
			for k := range a.wakes {
				if a.wakes[k] > a.seen {
					oa[k] = a.wakes[k]
				}
				if b.wakes[k] > b.seen {
					ob[k] = b.wakes[k]
				}
			}
			if oa != ob {
				out = append(out, [3]string{"wakes (owed)", fmt.Sprint(oa), fmt.Sprint(ob)})
			}
			continue
		}
		if f.Type == invTimeT {
			ta := *(*time.Time)(unsafe.Pointer(fa.UnsafeAddr()))
			tb := *(*time.Time)(unsafe.Pointer(fb.UnsafeAddr()))
			d := ta.Sub(tb)
			if d < 0 {
				d = -d
			}
			// The loop reads its own clock a moment from the event's stamp.
			if ta.IsZero() != tb.IsZero() || d > 50*time.Millisecond {
				out = append(out, [3]string{f.Name, ta.Format(time.RFC3339Nano), tb.Format(time.RFC3339Nano)})
			}
			continue
		}
		ia := reflect.NewAt(f.Type, unsafe.Pointer(fa.UnsafeAddr())).Elem().Interface()
		ib := reflect.NewAt(f.Type, unsafe.Pointer(fb.UnsafeAddr())).Elem().Interface()
		if !reflect.DeepEqual(ia, ib) {
			out = append(out, [3]string{f.Name, invShow(ia), invShow(ib)})
		}
	}
	return out
}

func invShow(v any) string {
	s := fmt.Sprintf("%+v", v)
	if rv := reflect.ValueOf(v); rv.Kind() == reflect.Ptr && !rv.IsNil() {
		s = fmt.Sprintf("&%+v", rv.Elem().Interface())
	}
	if len(s) > 120 {
		s = s[:120] + "..."
	}
	return s
}

// ---- 2: each request only adds to the one before ---------------------------

// invRegister notes a fake model server and the test that made it.
func invRegister(s *scripted) {
	pcs := make([]uintptr, 64)
	frames := runtime.CallersFrames(pcs[:runtime.Callers(2, pcs)])
	name := "a test"
	for {
		fr, more := frames.Next()
		if i := strings.LastIndex(fr.Function, ".Test"); i >= 0 {
			name = fr.Function[i+1:]
			if j := strings.Index(name, "."); j >= 0 {
				name = name[:j]
			}
		}
		if !more {
			break
		}
	}
	inv.mu.Lock()
	inv.servers = append(inv.servers, invServer{test: name, s: s})
	inv.mu.Unlock()
}

// invSharedServer names tests whose requests on one fake server are not one
// conversation after another.
var invSharedServer = map[string]string{
	"TestCoreTwoSessionsInOneProject":             "two sessions talk to one fake server at once",
	"TestTwoRoutesToTheSameModelAreEachTriedOnce": "two routes with their own settings reach one model name",
}

var invLookRe = regexp.MustCompile(`^\[eagent( \d\d:\d\d(:\d\d)?)?\]`)

func invMsg(raw json.RawMessage) (role, text string) {
	var m struct {
		Role    string `json:"role"`
		Content any    `json:"content"`
	}
	_ = json.Unmarshal(raw, &m)
	return m.Role, fmt.Sprint(m.Content)
}

func invHash(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:8])
}

// invSentBack are the words of a correction the loop sends with a reply it
// did not accept. Once a reply is accepted, the one sent back and its
// correction leave the conversation.
var invSentBack = []string{
	"not accepted:",
	"That reply had no tool call",
	"The user's message was kept for you",
	"The user's answer has reached nobody but you",
}

// invRewrite says why a request that does not extend the one before it is
// allowed to differ, or "" when nothing allows it. at is the first message
// that differs.
func invRewrite(prev, next []json.RawMessage, at int) string {
	nextText := ""
	if at < len(next) {
		_, nextText = invMsg(next[at])
	}
	switch {
	case strings.Contains(nextText, "[Briefing."):
		return "a restart from a dossier begins a new conversation"
	case strings.Contains(nextText, "Write that briefing now") && len(next) <= 3:
		return "a dossier pass is its own conversation"
	case strings.Contains(nextText, "could not be shown to this model: the provider refused it"):
		return "a picture the provider refused is replaced by a note"
	case strings.HasPrefix(nextText, "Current title"):
		return "a summary call is one request with its own prompt"
	}
	for _, m := range next[min(at, len(next)):] {
		if _, text := invMsg(m); strings.Contains(text, "What you looked up last did not fit") {
			return "lookups that made a dossier pass too long are taken out of it"
		}
	}
	// What the earlier request held from the first difference on that the
	// later one holds nowhere.
	var lost []json.RawMessage
	for _, m := range prev[min(at, len(prev)):] {
		found := false
		for _, n := range next {
			if bytes.Equal(m, n) {
				found = true
				break
			}
		}
		if !found {
			lost = append(lost, m)
		}
	}
	for _, m := range lost {
		_, text := invMsg(m)
		for _, words := range invSentBack {
			if strings.Contains(text, words) {
				return "a reply sent back and its correction leave once a reply is accepted"
			}
		}
	}
	isLook := func(m json.RawMessage) bool {
		role, text := invMsg(m)
		return role == "user" && invLookRe.MatchString(text)
	}
	if len(lost) == 0 {
		if at == len(prev)-1 && isLook(prev[at]) {
			return "the prompt of a look is not kept"
		}
		return ""
	}
	if !isLook(lost[0]) {
		return ""
	}
	for _, m := range lost[1:] {
		role, _ := invMsg(m)
		hold := role == "assistant" && bytes.Contains(m, []byte(`"name":"hold"`))
		if !hold && role != "tool" && !isLook(m) {
			return ""
		}
	}
	return "the prompt of a look, and a hold that left no record, are not kept"
}

// invRequests checks every fake model server of the run.
func invRequests() {
	inv.mu.Lock()
	servers := inv.servers
	inv.servers = nil
	inv.mu.Unlock()
	for _, sv := range servers {
		if _, ok := invSharedServer[sv.test]; ok {
			continue
		}
		sv.s.mu.Lock()
		reqs := append([]request(nil), sv.s.reqs...)
		sv.s.mu.Unlock()
		last := map[string]request{}
		for _, q := range reqs {
			if len(q.Messages) == 0 {
				continue
			}
			if role, _ := invMsg(q.Messages[0]); role != "system" {
				continue // a one-shot call with its own prompt (the summary)
			}
			var rest struct {
				Key string `json:"prompt_cache_key"`
			}
			_ = json.Unmarshal([]byte(q.Rest), &rest)
			// One conversation: a model, a system prompt, a session's cache key.
			lane := q.Model + "|" + invHash(q.Messages[0]) + "|" + rest.Key
			prev, seen := last[lane]
			last[lane] = q
			inv.mu.Lock()
			inv.requests++
			inv.mu.Unlock()
			if !seen {
				continue
			}
			if !bytes.Equal(prev.Tools, q.Tools) {
				invFail("%s: the tool list changed inside one conversation with %s", sv.test, q.Model)
			}
			if prev.Rest != q.Rest {
				invFail("%s: the request options changed inside one conversation with %s: %s then %s", sv.test, q.Model, prev.Rest, q.Rest)
			}
			at := 0
			for at < len(prev.Messages) && at < len(q.Messages) && bytes.Equal(prev.Messages[at], q.Messages[at]) {
				at++
			}
			if at == len(prev.Messages) {
				continue // it only adds
			}
			if invRewrite(prev.Messages, q.Messages, at) == "" {
				was := string(prev.Messages[at])
				if len(was) > 200 {
					was = was[:200] + "..."
				}
				invFail("%s: a request to %s rewrote message %d of the %d the one before it held, and no rule allows it: %s", sv.test, q.Model, at, len(prev.Messages), was)
			}
		}
	}
}

// The rules of the second check, on conversations made by hand: what must be
// allowed is, and a changed earlier message is not.
func TestARequestMayOnlyRewriteWhatARuleAllows(t *testing.T) {
	msg := func(role, text string) json.RawMessage {
		b, _ := json.Marshal(map[string]any{"role": role, "content": text})
		return b
	}
	sys, hello, reply := msg("system", "You are eagent."), msg("user", "hello"), msg("assistant", "Hi.")
	look := msg("user", "[eagent 10:04] Codex has stopped.")
	hold := json.RawMessage(`{"role":"assistant","content":null,"tool_calls":[{"function":{"name":"hold","arguments":"{}"}}]}`)
	held := msg("tool", "ok")
	for _, c := range []struct {
		name       string
		prev, next []json.RawMessage
		allowed    bool
	}{
		{"a look's prompt and its hold are dropped", []json.RawMessage{sys, hello, reply, look, hold, held}, []json.RawMessage{sys, hello, reply, msg("user", "next")}, true},
		{"a look's prompt alone is replaced", []json.RawMessage{sys, hello, reply, look}, []json.RawMessage{sys, hello, reply, msg("user", "next")}, true},
		{"a restart from a dossier", []json.RawMessage{sys, hello, reply}, []json.RawMessage{sys, msg("user", "[Briefing. What happened so far]")}, true},
		{"a reply sent back leaves with its correction", []json.RawMessage{sys, hello, msg("assistant", "x"), msg("user", "That reply had no tool call.")}, []json.RawMessage{sys, hello, reply}, true},
		{"the user's first message is rewritten", []json.RawMessage{sys, hello, reply}, []json.RawMessage{sys, msg("user", "hello there"), reply}, false},
		{"an earlier reply is dropped", []json.RawMessage{sys, hello, reply, msg("user", "more")}, []json.RawMessage{sys, hello, msg("user", "more")}, false},
		{"a reply that left a record goes with a look", []json.RawMessage{sys, hello, look, reply}, []json.RawMessage{sys, hello, msg("user", "next")}, false},
	} {
		at := 0
		for at < len(c.prev) && at < len(c.next) && bytes.Equal(c.prev[at], c.next[at]) {
			at++
		}
		if why := invRewrite(c.prev, c.next, at); (why != "") != c.allowed {
			t.Errorf("%s: allowed=%v (%q), want %v", c.name, why != "", why, c.allowed)
		}
	}
	// And the first check sees a field the record does not rebuild.
	a, b := turnState{saidSeq: 4, spokeSeen: 1}, turnState{saidSeq: 4}
	if d := invCompare(&a, &b); len(d) != 1 || d[0][0] != "spokeSeen" {
		t.Errorf("a field that differs was reported as %v", d)
	}
	a, b = turnState{failures: 2}, turnState{}
	if d := invCompare(&a, &b); len(d) != 0 {
		t.Errorf("a field listed as rebuilt otherwise was reported: %v", d)
	}
}
