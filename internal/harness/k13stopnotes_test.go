package harness

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/store"
)

// sr6StopProject is a project whose file turns the phone off, with a saved
// preset "phone" that turns it on at a fake service: `eagent serve --config
// phone` runs every session it hosts on it (Server.loadConfig lays s.Bundle),
// so each of them mirrors to the phone. It returns the project, a session
// record in it, and the count of posts the service has had.
func sr6StopProject(t *testing.T, fileRaw string) (project, path string, posts *atomic.Int32) {
	t.Helper()
	t.Setenv("EAGENT_FINALECHAT", "")
	t.Setenv("EAGENT_CONFIG", "")
	t.Setenv("EAGENT_PRESET", "")
	posts = new(atomic.Int32)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("SR6_FC_TOKEN", "tok")
	project = t.TempDir()
	if err := os.MkdirAll(config.BundlesDir(project), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.File(project), []byte(fileRaw), 0o644); err != nil {
		t.Fatal(err)
	}
	phone := `{"finalechat":{"enabled":true,"base_url":"` + srv.URL + `","token_env":"SR6_FC_TOKEN"}}`
	if err := os.WriteFile(config.BundlePath(project, "phone"), []byte(phone), 0o644); err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(store.Root(project), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	path = sess.Path
	_ = sess.Close()
	return project, path, posts
}

// A Stop the server's shutdown records (stopAgentsOf), or the next server
// finishes, tells the phone through TellPhoneStopped. It loaded the
// project's default configuration, not the one the session ran: a server
// started with --config phone hosts every session on that preset, which
// mirrors to the phone, yet the stop's receipts and closing note went by
// the project file, which has the phone off, so nothing was said. Now the
// caller passes the configuration the session ran.
func TestSR6TellPhoneStoppedFollowsTheConfigurationTheSessionRan(t *testing.T) {
	project, path, posts := sr6StopProject(t, `{"finalechat":{"enabled":false}}`)
	ran, err := config.LoadBundle(project, "", "phone") // as `eagent serve --config phone` loads it
	if err != nil || !ran.Finalechat.Wanted() {
		t.Fatalf("setup: the session's configuration does not want the phone: %v", err)
	}
	TellPhoneStopped(ran, filepath.Clean(path), nil)
	if posts.Load() == 0 {
		t.Fatalf("a session that ran with the phone on (saved preset phone, the server's --config) was stopped from outside its run: the phone was told nothing (no receipt, no closing note)")
	}
}

// Control: with the same settings in the project's default configuration,
// the phone is told.
func TestSR6TellPhoneStoppedControl(t *testing.T) {
	project, path, posts := sr6StopProject(t, `{"default_config":"phone"}`)
	cfg, err := config.LoadBundle(project, "", "")
	if err != nil {
		t.Fatal(err)
	}
	TellPhoneStopped(cfg, path, nil)
	if posts.Load() == 0 {
		t.Fatalf("control: the phone was told nothing")
	}
}

// A session stopped from outside its run that still owes the sender a
// refusal note gets it posted before the closing note, recorded as posted
// on its record, and a second telling posts neither again.
func TestK13AStopToldFromOutsidePaysWhatTheSenderIsOwed(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	t.Setenv("EAGENT_TEST_FC", "fc_test")
	project := t.TempDir()
	cfg := phoneFakeConfig(t, project, "http://127.0.0.1:1", fp)
	sess, err := store.Create(store.Root(project), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	for _, ev := range []event.Event{
		event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{Session: sess.ID, Cwd: project}),
		event.New(phoneRefusedEvent, event.ActorHarness, phoneRefusedData{MessageID: "pm1", AttachmentID: "a1", Text: "IMG_1.png was not taken: it is over 10 MB."}),
	} {
		if _, err := sess.Append(ev); err != nil {
			t.Fatal(err)
		}
	}
	record := func(ev event.Event) {
		if _, err := sess.Append(ev); err != nil {
			t.Error(err)
		}
	}
	TellPhoneStopped(cfg, sess.Path, record)
	var kinds []string
	for _, p := range fp.postsWhere(func(map[string]any) bool { return true }) {
		meta, _ := p["meta"].(map[string]any)
		kind, _ := meta["kind"].(string)
		kinds = append(kinds, kind)
	}
	note, end := slices.Index(kinds, "attachment_refused"), slices.Index(kinds, "session_end")
	if note < 0 || end < 0 || note > end {
		t.Fatalf("posted %q; want the owed note, then the closing note", kinds)
	}
	evs, err := store.Read(sess.Path)
	if err != nil {
		t.Fatal(err)
	}
	if owed := owedRefusals(evs); len(owed) != 0 {
		t.Fatalf("the record still owes %+v after the note was posted", owed)
	}
	TellPhoneStopped(cfg, sess.Path, record)
	if n, m := len(fp.postsWhere(k1Kind("attachment_refused"))), len(fp.postsWhere(k1Kind("session_end"))); n != 1 || m != 1 {
		t.Fatalf("told twice: %d owed note(s), %d closing note(s), want one each", n, m)
	}
}

// sr6Header is the agent header the thread meta last carried ("relay"),
// and whether any patch carried one.
func sr6Header(fp *phoneFake) (any, bool) {
	var got any
	seen := false
	for _, m := range fp.metaPatches() {
		if v, ok := m["relay"]; ok {
			got, seen = v, true
		}
	}
	return got, seen
}

// A shutdown Stop (stopAgents, then TellPhoneStopped) of a session whose
// run the shutdown ended: the agent is stopped and its end recorded, and
// the phone is told what a run's own stop tells it. A run's own stop
// records relay.end on the loop, which clears the thread's agent header
// (observe -> publishRelayMeta, relay: nil). TellPhoneStopped patches only
// the receipts, so the header goes on showing the stopped agent as
// working, with its attach command and switch controls.
func TestSR6PhoneStopFromOutsideLeavesTheStoppedAgentInTheHeader(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	g := phoneStart(t, fp, "start work", true, func(string, []map[string]any) reply { return hold() }, nil)
	waitFor(t, "the phone poll", func() bool { return fp.getCount() > 0 })
	waitFor(t, "the agent", func() bool { return len(g.seat.sentTexts()) == 1 })
	waitFor(t, "the header shows the agent", func() bool { h, _ := sr6Header(fp); return h != nil })
	name := ""
	g.rt.sync(func() {
		if a := g.rt.st.ActiveRelays(); len(a) > 0 {
			name = a[0].Name
		}
	})
	if name == "" {
		t.Fatal("setup: no agent")
	}
	// The server's shutdown ends the run; the agent runs on.
	g.interrupt()
	// The Stop during the shutdown, as web.stopAgents records it.
	sess, _, err := store.Open(g.rt.sess.Path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.Append(event.New(event.RelayEnd, event.ActorHarness, event.RelayEndData{Name: name, Reason: "the user stopped the session"})); err != nil {
		t.Fatal(err)
	}
	sess.Close()
	t.Setenv("FINALECHAT_URL", fp.srv.URL)
	t.Setenv("FINALECHAT_TOKEN", "fc_test")
	closes := len(fp.postsWhere(k1Kind("session_end")))
	TellPhoneStopped(g.rt.cfg, g.rt.sess.Path, nil)
	if n := len(fp.postsWhere(k1Kind("session_end"))); n != closes+1 {
		t.Fatalf("setup: TellPhoneStopped posted %d closing notes", n-closes)
	}
	if h, _ := sr6Header(fp); h != nil {
		t.Fatalf("after the stop the phone's header still shows the stopped agent: %v", h)
	}
}
