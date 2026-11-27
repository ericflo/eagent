package harness

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
)

// sr5FlakyFiles is the phone fake behind a service that, for a few
// seconds, answers 503 to file downloads, and answers 503 to the refusal
// note of IMG_B.png on the first failNotes tries; once those are spent the
// files can be fetched again.
func sr5FlakyFiles(t *testing.T, fp *phoneFake, failNotes int32) (srvURL string, refusedA func() []string) {
	t.Helper()
	u, err := url.Parse(fp.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(u)
	var down atomic.Bool
	down.Store(true)
	var bTries atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fail := func() {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"code":"unavailable","message":"try later"}}`))
		}
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/attachments/") && down.Load() {
			fail()
			return
		}
		if r.Method == http.MethodPost && r.Body != nil {
			raw, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(raw))
			r.ContentLength = int64(len(raw))
			if bytes.Contains(raw, []byte("attachment_refused")) && bytes.Contains(raw, []byte("IMG_B.png")) {
				if n := bTries.Add(1); n <= failNotes {
					if n == failNotes {
						down.Store(false) // the service is back
					}
					fail()
					return
				}
			}
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	fp.mu.Lock()
	fp.files["att-a"] = phoneFakeFile{name: "IMG_A.png", contentType: "image/png", data: []byte("pngA")}
	fp.files["att-b"] = phoneFakeFile{name: "IMG_B.png", contentType: "image/png", data: []byte("pngB")}
	fp.mu.Unlock()
	return srv.URL, func() []string {
		var out []string
		for _, p := range fp.postsWhere(k1Kind("attachment_refused")) {
			if body, _ := p["body"].(string); strings.Contains(body, "IMG_A.png") {
				out = append(out, body)
			}
		}
		return out
	}
}

func sr5TwoFiles() map[string]any {
	return r10Msg("the two screenshots of the crash",
		map[string]any{"id": "att-a", "kind": "image", "content_type": "image/png", "filename": "IMG_A.png", "size": 4, "url": "/api/v1/attachments/att-a"},
		map[string]any{"id": "att-b", "kind": "image", "content_type": "image/png", "filename": "IMG_B.png", "size": 4, "url": "/api/v1/attachments/att-b"})
}

// k14Taken waits for the message phoneID to be taken and returns it.
func k14Taken(t *testing.T, g *phoneRig, phoneID string) event.UserMessageData {
	t.Helper()
	var got event.UserMessageData
	waitFor(t, "the message taken", func() bool {
		found := false
		g.rt.sync(func() {
			for _, ev := range g.rt.st.Events {
				var d event.UserMessageData
				if ev.Type == event.UserMessage && ev.Decode(&d) == nil && d.PhoneID == phoneID {
					got, found = d, true
				}
			}
		})
		return found
	})
	return got
}

// A message with two files, read while the service is failing: neither
// file can be fetched, the note refusing the first goes out, the note
// refusing the second cannot, and the message is held back for the next
// read (4174c94). By that read the service is back: both files are fetched
// and the message is taken with them. The phone still says the first was
// "not passed on" and asks for it again, though the agent has it.
func TestSR5PhoneAFileSaidNotPassedOnIsTakenAtTheNextRead(t *testing.T) {
	setFor(t, &phoneRetryStep, 10*time.Millisecond)
	fp := newPhoneFake(true)
	fp.features = []string{"activity", "dismiss", "attachments"} // a service without client keys: one try per post
	t.Cleanup(fp.srv.Close)
	srvURL, refusedA := sr5FlakyFiles(t, fp, 1)
	g := phoneStart(t, fp, "start work", true, func(string, []map[string]any) reply { return hold() }, func(c *config.Config) { c.Finalechat.BaseURL = srvURL })
	waitFor(t, "the phone poll", func() bool { return fp.getCount() > 0 })
	ids := r10Says(fp, sr5TwoFiles())
	got := k14Taken(t, g, ids[0])
	if notes := refusedA(); len(got.Attachments) == 2 && len(notes) > 0 {
		t.Fatalf("the message was taken with both files (%d attachments), yet the phone was told %q", len(got.Attachments), notes[0])
	}
}

// A file that cannot be fetched, read as the session begins to end (the
// person types /quit at the terminal while the fetch is out): the message
// is left for the next run and nothing is said. The next run fetches the
// file and takes it, and no note ever said it was not passed on.
func TestSR5PhoneAFileNotFetchedAsTheSessionEndsIsTakenByTheNextRunUntold(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	u, err := url.Parse(fp.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(u)
	var rig atomic.Pointer[phoneRig]
	var once atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/attachments/att-a" && once.CompareAndSwap(false, true) {
			g := rig.Load()
			g.ui.input <- "/quit"
			waitFor(t, "the session to be ending", func() bool {
				ending := true
				g.rt.sync(func() { ending = g.rt.ending })
				return ending
			})
			fp.mu.Lock() // fetched again later, the file is there
			fp.files["att-a"] = phoneFakeFile{name: "IMG_A.png", contentType: "image/png", data: []byte("pngA")}
			fp.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"no attachment"}}`))
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	g := phoneStart(t, fp, "start work", true, func(string, []map[string]any) reply { return hold() }, func(c *config.Config) { c.Finalechat.BaseURL = srv.URL })
	rig.Store(g)
	waitFor(t, "the phone poll", func() bool { return fp.getCount() > 0 })
	ids := r10Says(fp, r10Msg("the screenshot of the crash", map[string]any{"id": "att-a", "kind": "image", "content_type": "image/png", "filename": "IMG_A.png", "size": 4, "url": "/api/v1/attachments/att-a"}))
	if code := g.exit(10 * time.Second); code != 0 {
		t.Fatalf("exit code %d", code)
	}
	g.resume(Options{Interactive: true})
	got := k14Taken(t, g, ids[0])
	r14Drained(t, g)
	if notes := fp.postsWhere(k1Kind("attachment_refused")); len(notes) > 0 {
		t.Fatalf("the next run took the message with %d file(s), and the phone was told %q", len(got.Attachments), notes[0]["body"])
	}
	if len(got.Attachments) != 1 {
		t.Fatalf("the next run took the message with %d files, want its one", len(got.Attachments))
	}
}

// k14RefuseNotes is the phone fake behind a service that answers 503 to
// refusal notes while refusing is set; it counts the tries.
func k14RefuseNotes(t *testing.T, fp *phoneFake) (srvURL string, refusing *atomic.Bool, tries *atomic.Int32) {
	t.Helper()
	u, err := url.Parse(fp.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(u)
	refusing, tries = &atomic.Bool{}, &atomic.Int32{}
	refusing.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.Body != nil {
			raw, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(raw))
			r.ContentLength = int64(len(raw))
			if bytes.Contains(raw, []byte("attachment_refused")) && refusing.Load() {
				tries.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"error":{"code":"unavailable","message":"try later"}}`))
				return
			}
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, refusing, tries
}

// k14Posted is how many refusal notes the record holds as posted.
func k14Posted(g *phoneRig) int {
	n := 0
	for _, ev := range g.log() {
		var d phoneRefusedData
		if ev.Type == phoneRefusedEvent && ev.Decode(&d) == nil && d.Posted {
			n++
		}
	}
	return n
}

// Known limit B10: a file that cannot be fetched, whose refusal note the
// service does not take for a while. The words are taken without the file
// and the note is owed: it is tried again until it goes out, once, and the
// record says so. (4174c94 dropped it after one try once its hold ran out.)
func TestK14ARefusalNoteThatCannotBePostedYetIsPostedOnceItCan(t *testing.T) {
	setFor(t, &phoneRetryStep, 5*time.Millisecond)
	fp := newPhoneFake(true)
	fp.features = []string{"activity", "dismiss", "attachments"}
	t.Cleanup(fp.srv.Close)
	srvURL, refusing, tries := k14RefuseNotes(t, fp)
	g := phoneStart(t, fp, "start work", true, func(string, []map[string]any) reply { return hold() }, func(c *config.Config) { c.Finalechat.BaseURL = srvURL })
	waitFor(t, "the phone poll", func() bool { return fp.getCount() > 0 })
	ids := r10Says(fp, r10Msg("here is the screenshot of the error", map[string]any{"id": "att-9", "kind": "image", "content_type": "image/png", "filename": "IMG_9.png", "size": 3, "url": "/api/v1/attachments/att-9"}))
	if got := k14Taken(t, g, ids[0]); len(got.Attachments) != 0 {
		t.Fatalf("taken with %d files", len(got.Attachments))
	}
	waitFor(t, "the note tried a few times", func() bool { return tries.Load() >= 4 })
	refusing.Store(false)
	waitFor(t, "the note posted", func() bool { return len(fp.postsWhere(k1Kind("attachment_refused"))) == 1 })
	waitFor(t, "the note recorded as posted", func() bool { return k14Posted(g) == 1 })
	r14Drained(t, g)
	if n := len(fp.postsWhere(k1Kind("attachment_refused"))); n != 1 {
		t.Fatalf("the note was posted %d times", n)
	}
}

// The same note still owed when the session stops: the next run posts it
// once its thread is attached, and the record says so.
func TestK14ARefusalNoteOwedByAStoppedRunIsPostedByTheNext(t *testing.T) {
	setFor(t, &phoneRetryStep, 5*time.Millisecond)
	setFor(t, &phoneCloseWait, 200*time.Millisecond) // the note owed at the end is tried that long
	fp := newPhoneFake(true)
	fp.features = []string{"activity", "dismiss", "attachments"}
	t.Cleanup(fp.srv.Close)
	srvURL, refusing, tries := k14RefuseNotes(t, fp)
	g := phoneStart(t, fp, "start work", true, func(string, []map[string]any) reply { return hold() }, func(c *config.Config) { c.Finalechat.BaseURL = srvURL })
	waitFor(t, "the phone poll", func() bool { return fp.getCount() > 0 })
	ids := r10Says(fp, r10Msg("here is the screenshot of the error", map[string]any{"id": "att-9", "kind": "image", "content_type": "image/png", "filename": "IMG_9.png", "size": 3, "url": "/api/v1/attachments/att-9"}))
	k14Taken(t, g, ids[0])
	waitFor(t, "the note tried", func() bool { return tries.Load() >= 1 })
	g.interrupt()
	if n := k14Posted(g); n != 0 {
		t.Fatalf("setup: %d notes recorded as posted", n)
	}
	refusing.Store(false)
	g.resume(Options{Interactive: true})
	waitFor(t, "the note posted by the next run", func() bool { return len(fp.postsWhere(k1Kind("attachment_refused"))) == 1 })
	waitFor(t, "the note recorded as posted", func() bool { return k14Posted(g) == 1 })
	r14Drained(t, g)
	if n := len(fp.postsWhere(k1Kind("attachment_refused"))); n != 1 {
		t.Fatalf("the note was posted %d times", n)
	}
}

// On a service without client keys, while the files stay out of reach:
// the note for each file is posted once, however many tries the other
// file's note needed.
func TestSR5PhoneARefusalNoteIsPostedOncePerHeldRead(t *testing.T) {
	setFor(t, &phoneRetryStep, 10*time.Millisecond)
	fp := newPhoneFake(true)
	fp.features = []string{"activity", "dismiss", "attachments"}
	t.Cleanup(fp.srv.Close)
	srvURL, refusedA := sr5FlakyFiles(t, fp, 3)
	fp.mu.Lock()
	delete(fp.files, "att-a") // gone for good
	delete(fp.files, "att-b")
	fp.mu.Unlock()
	g := phoneStart(t, fp, "start work", true, func(string, []map[string]any) reply { return hold() }, func(c *config.Config) { c.Finalechat.BaseURL = srvURL })
	waitFor(t, "the phone poll", func() bool { return fp.getCount() > 0 })
	ids := r10Says(fp, sr5TwoFiles())
	k14Taken(t, g, ids[0])
	waitFor(t, "the notes", func() bool { return len(fp.postsWhere(k1Kind("attachment_refused"))) >= 2 })
	r14Drained(t, g)
	g.rt.sync(func() {})
	if notes := refusedA(); len(notes) > 1 {
		t.Fatalf("the phone was told %d times that IMG_A.png was not passed on", len(notes))
	}
}
