package config

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
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"m"}]}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

func redirectingTo(t *testing.T, target string) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target+r.URL.Path, http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestAModelListRedirectedToAnotherOriginNeverCarriesTheKey(t *testing.T) {
	t.Setenv("EAGENT_R5_TEST_KEY", "sk-secret")
	for _, id := range []string{"anthropic", "together"} {
		evil, got := keyThief(t)
		provider := redirectingTo(t, evil.URL)
		_, _ = probeProviderHTTP(context.Background(), Provider{ID: id, BaseURL: provider.URL, KeyEnv: "EAGENT_R5_TEST_KEY"})
		if s := got.Load().(string); strings.Contains(s, "sk-secret") {
			t.Errorf("%s: the key reached another origin: %q", id, s)
		}
	}
}

func TestAServerProbeRedirectedToAnotherOriginNeverCarriesTheKey(t *testing.T) {
	for _, anthropic := range []bool{true, false} {
		evil, got := keyThief(t)
		server := redirectingTo(t, evil.URL)
		_, _, _, _ = fetchRawModels(context.Background(), server.URL, "sk-secret", anthropic)
		if s := got.Load().(string); strings.Contains(s, "sk-secret") {
			t.Errorf("anthropic auth %v: the key reached another origin: %q", anthropic, s)
		}
	}
}

// A local server that moves /models on its own origin still answers.
func TestAServerProbeFollowsARedirectOnTheSameOrigin(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			http.Redirect(w, r, "/v1/models", http.StatusMovedPermanently)
			return
		}
		if r.Header.Get("Authorization") != "Bearer k" {
			http.Error(w, "no key", 401)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"m"}]}`))
	}))
	defer srv.Close()
	models, _, _, err := fetchRawModels(context.Background(), srv.URL, "k", false)
	if err != nil || len(models) != 1 {
		t.Fatalf("models %v, err %v", models, err)
	}
}
