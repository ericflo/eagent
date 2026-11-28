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
