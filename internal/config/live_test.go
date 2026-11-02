package config

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// stubProbe replaces the network probe for a test and returns a restore func.
func stubProbe(t *testing.T, fn func(ctx context.Context, pr Provider) ([]Model, error)) {
	t.Helper()
	old := liveProbe
	liveProbe = fn
	t.Cleanup(func() { liveProbe = old })
}

func TestLiveModelsServesFreshCacheWithoutNetwork(t *testing.T) {
	project := t.TempDir()
	cached := []Model{{Provider: "openrouter", ID: "x/y", Family: "y", Label: "Y", Live: true}}
	if err := WriteLiveCache(project, &LiveCache{FetchedAt: time.Now(), Models: cached}); err != nil {
		t.Fatal(err)
	}
	stubProbe(t, func(ctx context.Context, pr Provider) ([]Model, error) {
		t.Fatal("probe must not run while the cache is fresh")
		return nil, nil
	})
	res := LiveModels(context.Background(), project)
	if res.Refreshed || res.Stale || len(res.Models) != 1 || res.Models[0].ID != "x/y" {
		t.Fatalf("fresh cache not served as-is: %+v", res)
	}
	if res.FetchedAt.IsZero() {
		t.Fatal("no fetched_at")
	}
}

func TestLiveModelsRefreshesStaleCache(t *testing.T) {
	project := t.TempDir()
	old := []Model{{Provider: "openrouter", ID: "old/one", Label: "old", Live: true}}
	if err := WriteLiveCache(project, &LiveCache{FetchedAt: time.Now().Add(-25 * time.Hour), Models: old}); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	stubProbe(t, func(ctx context.Context, pr Provider) ([]Model, error) {
		calls.Add(1)
		if pr.ID == "openrouter" {
			return []Model{{Provider: "openrouter", ID: "z-ai/new-hotness", Family: "new-hotness", Label: "New Hotness", Live: true, Context: 1_000_000}}, nil
		}
		return nil, errors.New("no key") // providers without keys contribute nothing but are not fatal
	})
	res := LiveModels(context.Background(), project)
	if !res.Refreshed || res.Stale {
		t.Fatalf("expected a fresh refresh, got %+v", res)
	}
	if int(calls.Load()) != len(Providers) {
		t.Fatalf("probed %d providers, want %d", calls.Load(), len(Providers))
	}
	if len(res.Models) != 1 || res.Models[0].ID != "z-ai/new-hotness" {
		t.Fatalf("unexpected models: %+v", res.Models)
	}
	if res.Errors["openai"] == "" {
		t.Fatal("expected per-provider errors to be recorded")
	}
	// The cache on disk must now be fresh, so a second call does no network.
	stubProbe(t, func(ctx context.Context, pr Provider) ([]Model, error) {
		t.Fatal("second call should have been served from the refreshed cache")
		return nil, nil
	})
	res2 := LiveModels(context.Background(), project)
	if res2.Refreshed || len(res2.Models) != 1 {
		t.Fatalf("cache was not written by the refresh: %+v", res2)
	}
}

func TestLiveModelsFailedRefreshServesStaleAndDebounces(t *testing.T) {
	project := t.TempDir()
	old := []Model{{Provider: "openrouter", ID: "old/one", Label: "old", Live: true}}
	if err := WriteLiveCache(project, &LiveCache{FetchedAt: time.Now().Add(-48 * time.Hour), Models: old}); err != nil {
		t.Fatal(err)
	}
	stubProbe(t, func(ctx context.Context, pr Provider) ([]Model, error) {
		return nil, errors.New("offline")
	})
	res := LiveModels(context.Background(), project)
	if !res.Stale || res.Refreshed || len(res.Models) != 1 || res.Models[0].ID != "old/one" {
		t.Fatalf("expected stale cache served, got %+v", res)
	}
	// The failure must be stamped into the cache: the next call within 24h
	// does not hit the network again.
	stubProbe(t, func(ctx context.Context, pr Provider) ([]Model, error) {
		t.Fatal("failed refresh should be debounced for 24h")
		return nil, nil
	})
	res2 := LiveModels(context.Background(), project)
	if res2.Stale || res2.Refreshed || len(res2.Models) != 1 {
		t.Fatalf("failure was not debounced: %+v", res2)
	}
	// And with no cache at all, a failed refresh still writes a debouncing
	// empty cache rather than probing on every call.
	empty := t.TempDir()
	stubProbe(t, func(ctx context.Context, pr Provider) ([]Model, error) { return nil, errors.New("offline") })
	first := LiveModels(context.Background(), empty)
	if first.Stale || len(first.Models) != 0 || len(first.Errors) == 0 {
		t.Fatalf("empty project, failed refresh: %+v", first)
	}
	if _, err := os.Stat(LiveModelsPath(empty)); err != nil {
		t.Fatalf("empty cache should still be written to debounce: %v", err)
	}
	stubProbe(t, func(ctx context.Context, pr Provider) ([]Model, error) {
		t.Fatal("empty-cache failure should be debounced too")
		return nil, nil
	})
	LiveModels(context.Background(), empty)
}

func TestParseLiveModelsOpenRouterShape(t *testing.T) {
	pr, _ := ProviderByID("openrouter")
	body := []byte(`{"data":[
		{"id":"acme/nova-9","name":"Acme: Nova 9","context_length":2097152,
		 "pricing":{"prompt":"0.0000012","completion":"0.0000048","input_cache_read":"0.00000012"},
		 "architecture":{"input_modalities":["text","image"]},
		 "reasoning":{"mandatory":true,"supported_efforts":["max","high","weird","low"]}},
		{"id":"openai/gpt-6.2-future","name":"OpenAI: GPT-6.2","context_length":1050000,
		 "pricing":{"prompt":"0","completion":"0"}},
		{"id":"z-ai/glm-5.3","name":"Z AI: GLM 5.3","context_length":200000,"pricing":{"prompt":"0.0000014","completion":"0.0000044"}},
		{"id":""}
	]}`)
	ms, err := parseLiveModels(pr, body)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 3 {
		t.Fatalf("got %d models, want 3 (empty id skipped): %+v", len(ms), ms)
	}
	nova := ms[0]
	if nova.ID != "acme/nova-9" || nova.Label != "Nova 9" || !nova.Live {
		t.Fatalf("nova: %+v", nova)
	}
	if nova.Price == nil || nova.Price.In != 1.2 || nova.Price.Out != 4.8 || nova.Price.Cached != 0.12 {
		t.Fatalf("price not per-million: %+v", nova.Price)
	}
	if nova.Context != 2_097_152 || !nova.Vision {
		t.Fatalf("context/vision: %+v", nova)
	}
	// Efforts filtered to the canonical scale and sorted; "weird" dropped.
	if len(nova.Efforts) != 3 || nova.Efforts[0] != "low" || nova.Efforts[2] != "max" {
		t.Fatalf("efforts: %v", nova.Efforts)
	}
	if nova.Family != "nova-9" {
		t.Fatalf("family: %q", nova.Family)
	}
	// Unknown openai/* id gets the Responses protocol heuristic.
	if ms[1].Protocol != "openai-responses" {
		t.Fatalf("protocol heuristic: %q", ms[1].Protocol)
	}
	// Zero price means no price, not "free".
	if ms[1].Price != nil {
		t.Fatalf("zero pricing should parse as no price: %+v", ms[1].Price)
	}
	// A statically known id keeps its static metadata and is just marked live.
	glm := ms[2]
	if glm.ID != "z-ai/glm-5.3" || !glm.Live || glm.Family != "glm-5.3" || glm.Price == nil || glm.Price.In != 1.40 {
		t.Fatalf("static model should keep static metadata: %+v", glm)
	}
}

func TestParseLiveModelsBareArrayAndEnvelope(t *testing.T) {
	openai, _ := ProviderByID("openai")
	// OpenAI-style {"data":[{"id":...}]} with display names absent.
	ms, err := parseLiveModels(openai, []byte(`{"object":"list","data":[{"id":"gpt-6.2"},{"id":"gpt-6.2"}]}`))
	if err != nil || len(ms) != 1 || ms[0].ID != "gpt-6.2" || !ms[0].Live || ms[0].Label != "gpt-6.2" {
		t.Fatalf("envelope: %+v %v", ms, err)
	}
	// Bare array (Together-style) with display_name.
	ms, err = parseLiveModels(openai, []byte(`[{"id":"ft:gpt-x","display_name":"Fine-tuned X"}]`))
	if err != nil || len(ms) != 1 || ms[0].Label != "Fine-tuned X" {
		t.Fatalf("bare array: %+v %v", ms, err)
	}
	if _, err := parseLiveModels(openai, []byte(`{"nope":true}`)); err == nil {
		t.Fatal("garbage should error")
	}
}

func TestMergeLive(t *testing.T) {
	static := []Model{
		{Provider: "openrouter", ID: "z-ai/glm-5.3", Family: "glm-5.3", Label: "GLM-5.3"},
		{Provider: "openai", ID: "gpt-6-astra", Family: "gpt-6-astra", Label: "GPT-6 Astra"},
	}
	live := []Model{
		{Provider: "openrouter", ID: "z-ai/glm-5.3", Live: true},                 // known: flag only
		{Provider: "openrouter", ID: "acme/nova-9", Label: "Nova 9", Live: true}, // new: appended
	}
	out := MergeLive(static, live)
	if len(out) != 3 {
		t.Fatalf("want 3 models, got %d", len(out))
	}
	if !out[0].Live || out[0].Label != "GLM-5.3" {
		t.Fatalf("static entry should be flagged live, not replaced: %+v", out[0])
	}
	if out[1].Live {
		t.Fatal("untouched static entry must stay non-live")
	}
	if out[2].ID != "acme/nova-9" || !out[2].Live {
		t.Fatalf("live-only model appended: %+v", out[2])
	}
	// The input slice must not be mutated.
	if static[0].Live {
		t.Fatal("MergeLive mutated its input")
	}
}

func TestLiveModelsPathIsInProjectAgentsDir(t *testing.T) {
	p := LiveModelsPath("/tmp/proj")
	if p != filepath.Join("/tmp/proj", ".agents", "eagent", "live-models.json") {
		t.Fatalf("path: %s", p)
	}
	if !strings.HasSuffix(LiveModelsPath(t.TempDir()), "live-models.json") {
		t.Fatal("cache file name")
	}
}

// The cache file must round-trip through JSON with the live flag intact.
func TestLiveCacheRoundTrip(t *testing.T) {
	project := t.TempDir()
	c := &LiveCache{FetchedAt: time.Now().Truncate(time.Second).UTC(), Models: []Model{{Provider: "openrouter", ID: "a/b", Label: "B", Live: true}}, Errors: map[string]string{"openai": "no OPENAI_API_KEY"}}
	if err := WriteLiveCache(project, c); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(LiveModelsPath(project))
	var back LiveCache
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.Models) != 1 || !back.Models[0].Live || back.Errors["openai"] == "" || back.FetchedAt.IsZero() {
		t.Fatalf("round trip: %+v", back)
	}
}
