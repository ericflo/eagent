package harness

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/llm"
	"github.com/ericflo/eagent/internal/store"
)

// A provider the network cannot reach is waited for, not given up on: the
// call goes through once the network is back, with no restart.
func TestModelCallsWaitForTheNetwork(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	old := offlineWait
	offlineWait = 20 * time.Millisecond
	defer func() { offlineWait = old }()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close() // nothing listens: connection refused, as with the network down
	cfg := testConfig("http://" + addr)
	rt, err := New(cfg, Options{Project: t.TempDir()}, &fakeUI{input: make(chan string)})
	if err != nil {
		t.Fatal(err)
	}
	rt.narrClient.MaxAttempts = 1
	stop := make(chan struct{})
	defer close(stop)
	go func() { // stand in for the session loop
		for {
			select {
			case fn := <-rt.loop:
				fn()
			case <-stop:
				return
			}
		}
	}()
	model := newScripted(func(string, []map[string]any) reply { return reply{text: "back"} })
	model.srv.Close()
	go func() {
		time.Sleep(300 * time.Millisecond)
		l, err := net.Listen("tcp", addr)
		if err != nil {
			t.Error(err)
			return
		}
		srv := httptest.NewUnstartedServer(model.srv.Config.Handler)
		srv.Listener = l
		srv.Start()
		<-stop
		srv.Close()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := rt.completeActor(ctx, event.ActorNarrator, "", llm.Request{Messages: []llm.Message{{Role: "user", Text: "hi"}}})
	if err != nil || resp.Text != "back" {
		t.Fatalf("call after the network came back: %v %+v", err, resp)
	}
}

// FinaleChat failing when a session starts (5xx for a few seconds) delays the
// phone mirror; it no longer switches it off for the life of the process.
func TestPhoneMirrorSurvivesAnOutageAtStart(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	t.Setenv("EAGENT_TEST_FC", "fc_test")
	project := t.TempDir()
	s := newScripted(func(model string, msgs []map[string]any) reply {
		if model == "orch" {
			return reply{calls: []event.ToolCall{tc("yield", `{"done":false,"reason":"waiting"}`)}}
		}
		return reply{calls: []event.ToolCall{tc("hold", `{}`)}}
	})
	defer s.srv.Close()
	fp := newFakePhone(false)
	defer fp.srv.Close()
	var down atomic.Bool
	down.Store(true)
	inner := fp.srv.Config.Handler
	fp.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if down.Load() {
			w.WriteHeader(503)
			_, _ = w.Write([]byte(`{"error":{"code":"unavailable","message":"upstream down"}}`))
			return
		}
		inner.ServeHTTP(w, r)
	})
	ui := &fakeUI{input: make(chan string)}
	rt, err := New(fakePhoneConfig(t, project, s.srv.URL, fp), Options{Project: project, Interactive: true, Prompt: "hello"}, ui)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	done := make(chan int)
	go func() { done <- rt.Run(ctx) }()
	time.Sleep(5 * time.Second) // longer than the old three tries
	down.Store(false)
	for deadline := time.Now().Add(30 * time.Second); fp.postCount() == 0; {
		if time.Now().After(deadline) {
			t.Fatal("the mirror never posted after FinaleChat came back")
		}
		time.Sleep(50 * time.Millisecond)
	}
	fp.userReply("this comes from the phone")
	delivered := func() bool {
		evs, _ := store.Read(rt.sess.Path)
		for _, ev := range evs {
			var d event.UserMessageData
			if ev.Type == event.UserMessage && ev.Decode(&d) == nil && d.Source == "finalechat" {
				return true
			}
		}
		return false
	}
	for deadline := time.Now().Add(30 * time.Second); !delivered(); {
		if time.Now().After(deadline) {
			var off bool
			rt.sync(func() { off = rt.phone == nil })
			t.Fatalf("the phone reply never reached the session (mirror off: %v)", off)
		}
		time.Sleep(100 * time.Millisecond)
	}
	ui.input <- "/quit"
	<-done
}

// After model calls keep failing (a provider outage that answers 5xx), the
// paused orchestrator tries again by itself; no resume or restart needed.
func TestOrchestratorRetriesByItselfAfterFailures(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	old := errorRetryDelays
	errorRetryDelays = []time.Duration{200 * time.Millisecond}
	defer func() { errorRetryDelays = old }()
	var orchCalls atomic.Int32
	s := newScripted(func(model string, msgs []map[string]any) reply {
		if model == "orch" {
			return reply{calls: []event.ToolCall{tc("yield", `{"done":false,"reason":"recovered"}`)}}
		}
		return reply{calls: []event.ToolCall{tc("hold", `{}`)}}
	})
	defer s.srv.Close()
	inner := s.srv.Config.Handler
	s.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), `"model":"orch"`) && orchCalls.Add(1) <= 2 {
			w.WriteHeader(503)
			_, _ = w.Write([]byte(`{"error":{"message":"overloaded"}}`))
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		inner.ServeHTTP(w, r)
	})
	ui := &fakeUI{input: make(chan string)}
	rt, err := New(testConfig(s.srv.URL), Options{Project: t.TempDir(), Interactive: true, Prompt: "do it"}, ui)
	if err != nil {
		t.Fatal(err)
	}
	rt.orchClient.MaxAttempts = 1
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	done := make(chan int)
	go func() { done <- rt.Run(ctx) }()
	recovered := func() bool {
		evs, _ := store.Read(rt.sess.Path)
		for _, ev := range evs {
			var d event.YieldData
			if ev.Type == event.Yield && ev.Decode(&d) == nil && d.Reason == "recovered" {
				return true
			}
		}
		return false
	}
	for deadline := time.Now().Add(30 * time.Second); !recovered(); {
		if time.Now().After(deadline) {
			t.Fatalf("the orchestrator never retried (calls: %d)", orchCalls.Load())
		}
		time.Sleep(50 * time.Millisecond)
	}
	ui.input <- "/quit"
	<-done
}
