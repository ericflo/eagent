package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// keyThief is a server at another origin (another port) that a provider's
// answer redirects to; it keeps whatever credential reaches it.
func keyThief(t *testing.T) (*httptest.Server, *atomic.Value) {
	var got atomic.Value
	got.Store("")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.Store(r.Header.Get("x-api-key") + r.Header.Get("Authorization"))
		anthropicSSE(w, "ok")
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

func redirectingTo(t *testing.T, target string) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestAModelCallRedirectedToAnotherOriginNeverCarriesTheKey(t *testing.T) {
	for _, proto := range []string{ProtocolAnthropic, ProtocolChat, ProtocolResponses} {
		evil, got := keyThief(t)
		provider := redirectingTo(t, evil.URL)
		c := NewClient(Endpoint{Protocol: proto, BaseURL: provider.URL, Model: "m", APIKey: "sk-secret"})
		c.MaxAttempts = 1
		_, _ = c.Complete(context.Background(), Request{System: "sys", Messages: []Message{{Role: "user", Text: "hi"}}}, nil)
		if s := got.Load().(string); strings.Contains(s, "sk-secret") {
			t.Errorf("%s: the key reached another origin through a redirect: %q", proto, s)
		}
	}
}

// A local server that moves a path on its own origin (a trailing slash, a
// /v1 prefix) keeps working.
func TestAModelCallFollowsARedirectOnTheSameOrigin(t *testing.T) {
	var key atomic.Value
	key.Store("")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/real") {
			http.Redirect(w, r, "/real"+r.URL.Path, http.StatusPermanentRedirect)
			return
		}
		key.Store(r.Header.Get("x-api-key"))
		anthropicSSE(w, "ok")
	}))
	defer srv.Close()
	c := NewClient(Endpoint{Protocol: ProtocolAnthropic, BaseURL: srv.URL, Model: "m", APIKey: "k"})
	c.MaxAttempts = 1
	resp, err := c.Complete(context.Background(), Request{System: "sys", Messages: []Message{{Role: "user", Text: "hi"}}}, nil)
	if err != nil || resp.Text != "ok" || key.Load() != "k" {
		t.Fatalf("resp %+v, err %v, key %q", resp, err, key.Load())
	}
}
