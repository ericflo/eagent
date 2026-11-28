package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/finalechat"
	"github.com/ericflo/eagent/internal/state"
)

// queuePhone is a phone with no worker running: what it queues waits until
// the test drains it, so the queue can be filled to the slot. refuse, when
// set, makes the server refuse a post whose body it matches.
type queuePhone struct {
	p      *phone
	mu     sync.Mutex
	posts  []finalechat.PostRequest
	logs   []string
	refuse func(body string) bool
}

func newQueuePhone(t *testing.T, size int) *queuePhone {
	t.Helper()
	q := &queuePhone{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req finalechat.PostRequest
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &req)
		w.Header().Set("Content-Type", "application/json")
		q.mu.Lock()
		refused := q.refuse != nil && q.refuse(req.Body)
		if !refused {
			q.posts = append(q.posts, req)
		}
		n := len(q.posts)
		q.mu.Unlock()
		if refused {
			w.WriteHeader(422)
			_, _ = w.Write([]byte(`{"error":{"code":"invalid","message":"refused"}}`))
			return
		}
		w.WriteHeader(201)
		fmt.Fprintf(w, `{"message":{"id":"m%d"},"thread":{"id":"t1"}}`, n)
	}))
	t.Cleanup(srv.Close)
	q.p = &phone{
		client: &finalechat.Client{BaseURL: srv.URL, Token: "fc_test"}, ref: "ext:eagent:x", sid: "x",
		features: featureSet([]string{"idempotency", "activity"}), queue: make(chan func(context.Context), size),
		posted: map[string]bool{}, questions: map[string]string{}, fromPhone: map[string]string{}, open: map[string]string{}, gone: map[string]bool{},
		ctx: context.Background(),
		logf: func(format string, args ...any) {
			q.mu.Lock()
			q.logs = append(q.logs, fmt.Sprintf(format, args...))
			q.mu.Unlock()
		},
	}
	return q
}

// fill leaves free slots in the queue.
func (q *queuePhone) fill(free int) {
	for len(q.p.queue) < cap(q.p.queue)-free {
		q.p.queue <- func(context.Context) {}
	}
}

// drain runs what is queued, in order, as the worker would.
func (q *queuePhone) drain() {
	for len(q.p.queue) > 0 {
		(<-q.p.queue)(context.Background())
	}
}

func (q *queuePhone) reportPosts() []finalechat.PostRequest {
	q.mu.Lock()
	defer q.mu.Unlock()
	var out []finalechat.PostRequest
	for _, p := range q.posts {
		if p.Meta["kind"] == "relay_report" {
			out = append(out, p)
		}
	}
	return out
}

func (q *queuePhone) logged() string {
	q.mu.Lock()
	defer q.mu.Unlock()
	return strings.Join(q.logs, "\n")
}

func reportTurn(seq int64, text string) event.Event {
	ev := event.New(event.RelayTurn, event.ActorHarness, event.RelayTurnData{Name: "codex", Text: text, DurationMS: 1000, Tools: 2})
	ev.Seq = seq
	return ev
}

// twoParts is a report that takes two posts.
var twoParts = strings.Repeat(strings.Repeat("x", 99)+"\n", 1500) + "END"

// With no slot free the report is not queued at all, and that is said:
// no part goes out alone.
func TestReportThatFindsTheQueueFullIsDroppedWholeAndSaid(t *testing.T) {
	q := newQueuePhone(t, 16)
	q.fill(0)
	r := &Runtime{st: state.New(), relayNotifyDone: map[string]bool{}}
	q.p.observeRelay(r, reportTurn(7, twoParts))
	q.drain()
	if n := len(q.reportPosts()); n != 0 {
		t.Fatalf("%d parts went out", n)
	}
	if !strings.Contains(q.logged(), "report") {
		t.Fatalf("nothing said about the lost report: %q", q.logged())
	}
}

// A single report takes the path it always took: one post, its key, no
// part fields.
func TestSingleReportIsOnePost(t *testing.T) {
	q := newQueuePhone(t, 16)
	r := &Runtime{st: state.New(), relayNotifyDone: map[string]bool{}}
	q.p.observeRelay(r, reportTurn(3, "Fixed it."))
	q.drain()
	posts := q.reportPosts()
	if len(posts) != 1 || posts[0].Body != "Fixed it." || posts[0].ClientKey != "eagent:x:rt:3" || posts[0].Meta["part"] != nil || posts[0].Meta["duration_ms"] != float64(1000) {
		t.Fatalf("posts %+v", posts)
	}
}

// A question whose card could not be queued does not keep a session with
// no terminal waiting on the phone for an answer nobody can give.
func TestQuestionTheFullQueueDroppedDoesNotKeepTheSessionWaiting(t *testing.T) {
	q := newQueuePhone(t, 4)
	q.fill(0)
	st := state.New()
	st.Apply(event.New(event.Question, event.ActorEagent, event.QuestionData{ID: "q1", Text: "Merge it?"}))
	r := &Runtime{st: st, phone: q.p}
	q.p.ask(r, "q1", "Merge it?", nil)
	if r.waitingOnPhone() {
		t.Fatal("the session waits on a card that was never posted")
	}
	if !strings.Contains(q.logged(), "question") {
		t.Fatalf("nothing said about the lost card: %q", q.logged())
	}
}

// A phone that is slow, not down, takes one post now and then: the queue
// stays about full, one item gets in and the next is dropped. That is one
// stretch of dropping, said once, until the queue has really drained.
func TestASlowPhoneIsSaidToBeDroppingOncePerStretch(t *testing.T) {
	q := newQueuePhone(t, 8)
	q.fill(0)
	said := func() int {
		return strings.Count(q.logged(), "updates to the thread are being dropped")
	}
	q.p.enqueue(func(context.Context) {})
	if said() != 1 {
		t.Fatalf("a full queue was said %d times, want once", said())
	}
	for i := 0; i < 3; i++ {
		(<-q.p.queue)(context.Background()) // the phone takes one
		if !q.p.enqueue(func(context.Context) {}) {
			t.Fatal("a free slot was not used")
		}
		if q.p.enqueue(func(context.Context) {}) {
			t.Fatal("a full queue took an item")
		}
	}
	if said() != 1 {
		t.Fatalf("one stretch of dropping was said %d times, want once", said())
	}
	q.drain()
	q.p.enqueue(func(context.Context) {})
	q.fill(0)
	q.p.enqueue(func(context.Context) {})
	if said() != 2 {
		t.Fatalf("a new stretch after the queue drained was said %d times in all, want 2", said())
	}
}
