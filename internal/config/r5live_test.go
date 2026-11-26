package config

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// What LiveModels hands out is the caller's own: the server encodes it
// outside the cache's lock while a registered server's refresh rewrites
// the cache. A map shared between the two crashes the whole server.
func TestLiveModelsHandsOutErrorsTheCacheDoesNotShare(t *testing.T) {
	project := t.TempDir()
	if err := WriteLiveCache(project, &LiveCache{FetchedAt: time.Now(), Models: []Model{{Provider: "openrouter", ID: "x/y"}}, Errors: map[string]string{"together": "no key"}}); err != nil {
		t.Fatal(err)
	}
	stubProbe(t, func(context.Context, Provider) ([]Model, error) {
		t.Fatal("probe must not run while the cache is fresh")
		return nil, nil
	})
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			UpdateLiveCacheForServer(project, fmt.Sprintf("srv-%d", i%3), nil, "down")
			if i%2 == 0 {
				RemoveLiveCacheForServer(project, fmt.Sprintf("srv-%d", i%3))
			}
		}
	}()
	for i := 0; i < 200; i++ {
		res := LiveModels(context.Background(), project)
		if _, err := json.Marshal(res); err != nil {
			t.Fatal(err)
		}
		for range res.Errors {
		}
		if c := readLiveCache(project); c != nil {
			for range c.Errors {
			}
			for range c.Models {
			}
		}
	}
	close(stop)
	<-done
	if res := LiveModels(context.Background(), project); res.Errors["together"] != "no key" {
		t.Fatalf("errors = %v", res.Errors)
	}
}
