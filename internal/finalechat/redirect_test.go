package finalechat

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestCredentialsNeverFollowAPIRedirects(t *testing.T) {
	t.Setenv("EAGENT_FINALECHAT", "")
	var forwarded atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded.Add(1); w.WriteHeader(204) }))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/receive", http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	for _, token := range []string{"fc_fixture", "fcc_fixture"} {
		client := &Client{BaseURL: origin.URL, Token: token, HTTP: origin.Client()}
		for _, method := range []string{"GET", "POST", "PUT"} {
			err := client.Request(context.Background(), method, "/api/v1/fixture", nil, map[string]any{"fixture": true}, nil, 0)
			var apiError *Error
			if !errors.As(err, &apiError) || apiError.Status != 307 {
				t.Fatalf("%s redirect accepted: %v", method, err)
			}
		}
		_, _, err := client.Download(context.Background(), "/api/v1/attachments/fixture", io.Discard)
		var apiError *Error
		if !errors.As(err, &apiError) || apiError.Status != 307 {
			t.Fatalf("download redirect accepted: %v", err)
		}
	}
	if forwarded.Load() != 0 {
		t.Fatal("credentialed request followed a redirect to another port")
	}
}
