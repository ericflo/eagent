package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ericflo/eagent/internal/llm"
)

// Live model discovery: each catalog provider's /models endpoint is queried
// on demand — only when something asks for the live list (the web Config
// page does, when it loads) — never at startup, so eagent never blocks
// starting a session on a network call. Results are cached on disk for
// LiveTTL (24h): a fresh cache is served without any HTTP, and once it is
// older than 24h the next request refreshes it, so an eagent that runs for
// days or weeks still picks up newly released models. Even failed refreshes
// are stamped into the cache, so a provider outage cannot make every page
// load retry. Keys only ever travel to the provider's own catalog base URL,
// with the key variable the catalog already names.

const LiveTTL = 24 * time.Hour

// LiveModelsPath is the on-disk cache of the last live fetch, per project.
func LiveModelsPath(project string) string {
	return filepath.Join(project, ".agents", "eagent", "live-models.json")
}

// LiveCache is the persisted form of a live fetch.
type LiveCache struct {
	FetchedAt time.Time         `json:"fetched_at"`
	Models    []Model           `json:"models,omitempty"`
	Errors    map[string]string `json:"errors,omitempty"` // provider id -> why it contributed nothing
}

// LiveResult is what a caller of LiveModels gets.
type LiveResult struct {
	Models    []Model           `json:"models"`
	FetchedAt time.Time         `json:"fetched_at"`
	Refreshed bool              `json:"refreshed"` // a network refresh ran on this call
	Stale     bool              `json:"stale"`     // served from cache because the refresh found nothing
	Errors    map[string]string `json:"errors,omitempty"`
}

var (
	liveMu       sync.Mutex
	liveHTTP     = &http.Client{Timeout: 10 * time.Second}
	liveProbe    = probeProviderHTTP // replaced in tests
	liveMaxBytes = 8 << 20
)

// LiveModels returns the live model list for a project, refreshing it from
// the providers when the cache is missing or older than LiveTTL. It never
// runs at startup; only callers that explicitly ask (the settings page)
// trigger the HTTP fetches. The first call with a stale cache may block for
// up to ~20s; fresh-cache calls do no I/O beyond reading the cache file.
func LiveModels(ctx context.Context, project string) LiveResult {
	liveMu.Lock()
	defer liveMu.Unlock()
	cache := readLiveCache(project)
	if cache != nil && time.Since(cache.FetchedAt) < LiveTTL {
		return LiveResult{Models: cache.Models, FetchedAt: cache.FetchedAt, Errors: cache.Errors}
	}
	fresh := refreshLive(ctx)
	now := time.Now()
	if len(fresh.Models) > 0 {
		fresh.FetchedAt = now
		fresh.Refreshed = true
		_ = WriteLiveCache(project, &LiveCache{FetchedAt: now, Models: fresh.Models, Errors: fresh.Errors})
		return fresh
	}
	// The refresh found nothing (offline, no keys). Serve whatever was cached
	// and stamp the cache as just-checked so the failure is debounced too.
	if cache == nil {
		cache = &LiveCache{}
	}
	cache.FetchedAt = now
	cache.Errors = fresh.Errors
	_ = WriteLiveCache(project, cache)
	return LiveResult{Models: cache.Models, FetchedAt: now, Stale: cache.Models != nil, Errors: fresh.Errors}
}

// refreshLive probes every catalog provider concurrently and merges what
// came back, in provider order.
func refreshLive(ctx context.Context) LiveResult {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	type out struct {
		pr     Provider
		models []Model
		err    error
	}
	ch := make(chan out, len(Providers))
	for _, pr := range Providers {
		go func(pr Provider) {
			ms, err := liveProbe(ctx, pr)
			ch <- out{pr, ms, err}
		}(pr)
	}
	res := LiveResult{Errors: map[string]string{}}
	order := map[string]int{}
	for i, pr := range Providers {
		order[pr.ID] = i
	}
	for range Providers {
		o := <-ch
		if o.err != nil {
			res.Errors[o.pr.ID] = o.err.Error()
			continue
		}
		res.Models = append(res.Models, o.models...)
	}
	sort.SliceStable(res.Models, func(i, j int) bool {
		if order[res.Models[i].Provider] != order[res.Models[j].Provider] {
			return order[res.Models[i].Provider] < order[res.Models[j].Provider]
		}
		return res.Models[i].ID < res.Models[j].ID
	})
	return res
}

// probeProviderHTTP fetches one provider's /models list. Providers whose
// endpoint needs a key are skipped (not an error worth surfacing) when the
// key variable is unset; OpenRouter's endpoint is public.
func probeProviderHTTP(ctx context.Context, pr Provider) ([]Model, error) {
	url, keyHeader, extra, public := modelsEndpoint(pr)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	if !public {
		key := os.Getenv(pr.KeyEnv)
		if key == "" {
			return nil, fmt.Errorf("no %s in the environment", pr.KeyEnv)
		}
		if keyHeader == "x-api-key" {
			req.Header.Set("x-api-key", key)
		} else {
			req.Header.Set("Authorization", "Bearer "+key)
		}
	}
	resp, err := liveHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s -> %s", url, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(liveMaxBytes)))
	if err != nil {
		return nil, err
	}
	return parseLiveModels(pr, body)
}

// modelsEndpoint says where a provider lists its models and how the key
// travels. The URL is always derived from the catalog's own base URL, so a
// key can never be pointed at a host the catalog does not name.
func modelsEndpoint(pr Provider) (url, keyHeader string, extra map[string]string, public bool) {
	url = strings.TrimRight(pr.BaseURL, "/") + "/models"
	switch pr.ID {
	case "openrouter":
		return url, "", nil, true // public, no key needed
	case "anthropic":
		return url, "x-api-key", map[string]string{"anthropic-version": "2023-06-01"}, false
	default:
		return url, "Authorization", nil, false
	}
}

// rawLiveModel is the union of the fields the providers' /models endpoints
// actually return (OpenRouter's is the rich one; the rest mostly just ids).
type rawLiveModel struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	DisplayName   string `json:"display_name"`
	ContextLength int64  `json:"context_length"`
	Pricing       *struct {
		Prompt         string `json:"prompt"`
		Completion     string `json:"completion"`
		InputCacheRead string `json:"input_cache_read"`
	} `json:"pricing"`
	Architecture *struct {
		InputModalities []string `json:"input_modalities"`
	} `json:"architecture"`
	Reasoning *struct {
		Mandatory        bool     `json:"mandatory"`
		SupportedEfforts []string `json:"supported_efforts"`
	} `json:"reasoning"`
}

// parseLiveModels decodes a /models response body (either {"data": [...]} or
// a bare array) into catalog Models for that provider. Models already in the
// static catalog keep their static metadata (price, protocol, efforts) and
// are just marked live; unknown ids become bare live entries the editor can
// still pick. OpenRouter entries get the full treatment from their richer
// shape.
func parseLiveModels(pr Provider, body []byte) ([]Model, error) {
	var raws []json.RawMessage
	var env struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err == nil && len(env.Data) > 0 {
		raws = env.Data
	} else if err := json.Unmarshal(body, &raws); err == nil && len(raws) > 0 {
		// bare array
	} else {
		return nil, errors.New("unrecognized models response (no data array)")
	}
	seen := map[string]bool{}
	var out []Model
	for _, raw := range raws {
		var rm rawLiveModel
		if err := json.Unmarshal(raw, &rm); err != nil || rm.ID == "" {
			continue
		}
		if seen[rm.ID] {
			continue
		}
		seen[rm.ID] = true
		out = append(out, liveModelFor(pr, rm))
	}
	if len(out) == 0 {
		return nil, errors.New("no model ids in response")
	}
	return out, nil
}

// liveModelFor builds one live Model, preferring static catalog metadata when
// the id is already known.
func liveModelFor(pr Provider, rm rawLiveModel) Model {
	if m, ok := Lookup(pr.BaseURL, rm.ID); ok {
		m.Live = true
		return m
	}
	m := Model{Provider: pr.ID, ID: rm.ID, Family: familyOf(rm.ID), Live: true}
	label := rm.Name
	if label == "" {
		label = rm.DisplayName
	}
	// OpenRouter names look like "Z AI: GLM 5.3"; keep just the model part.
	if i := strings.LastIndex(label, ": "); i >= 0 {
		label = label[i+2:]
	}
	if label == "" {
		label = rm.ID
	}
	m.Label = label
	if pr.ID == "openrouter" {
		// The Responses-needing OpenAI models are the ones the static catalog
		// marks; for unknown openai/* ids assume the same.
		if strings.HasPrefix(rm.ID, "openai/") {
			m.Protocol = llm.ProtocolResponses
		}
		if rm.ContextLength > 0 {
			m.Context = int(rm.ContextLength)
		}
		if rm.Architecture != nil {
			for _, mod := range rm.Architecture.InputModalities {
				if mod == "image" || mod == "video" {
					m.Vision = true
				}
			}
		}
		if rm.Pricing != nil {
			if p, ok := livePrice(rm.Pricing); ok {
				m.Price = p
			}
		}
		if rm.Reasoning != nil && len(rm.Reasoning.SupportedEfforts) > 0 {
			for _, e := range rm.Reasoning.SupportedEfforts {
				if EffortIndex(e) < len(EffortOrder) {
					m.Efforts = append(m.Efforts, e)
				}
			}
		}
		if len(m.Efforts) > 0 {
			sort.Slice(m.Efforts, func(i, j int) bool { return EffortIndex(m.Efforts[i]) < EffortIndex(m.Efforts[j]) })
		}
	}
	return m
}

// livePrice turns OpenRouter's per-token price strings into USD per million.
func livePrice(pr *struct {
	Prompt         string `json:"prompt"`
	Completion     string `json:"completion"`
	InputCacheRead string `json:"input_cache_read"`
}) (*Price, bool) {
	perM := func(s string) float64 {
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0
		}
		return f * 1e6
	}
	in, cached, out := perM(pr.Prompt), perM(pr.InputCacheRead), perM(pr.Completion)
	if in == 0 && out == 0 {
		return nil, false
	}
	return &Price{In: in, Cached: cached, Out: out, AsOf: time.Now().Format("2006-01-02"), Source: "live GET /models"}, true
}

// familyOf derives a family from an id: the static catalog's family when the
// exact id is known anywhere, else the id without its org prefix.
func familyOf(id string) string {
	for _, m := range Models {
		if m.ID == id {
			return m.Family
		}
	}
	if i := strings.Index(id, "/"); i >= 0 {
		return strings.ToLower(id[i+1:])
	}
	return strings.ToLower(id)
}

// MergeLive overlays live results on the static catalog for display: known
// models are flagged live, unknown ones are appended. The static slice is
// not modified.
func MergeLive(static, live []Model) []Model {
	out := make([]Model, len(static))
	copy(out, static)
	byKey := map[string]int{}
	for i, m := range out {
		byKey[m.Provider+"|"+m.ID] = i
	}
	for _, m := range live {
		if i, ok := byKey[m.Provider+"|"+m.ID]; ok {
			out[i].Live = true
			continue
		}
		out = append(out, m)
	}
	return out
}

// ReadLiveCache returns the cached live models without any network I/O
// (unlike LiveModels, which may refresh a stale cache).
func ReadLiveCache(project string) *LiveCache { return readLiveCache(project) }

// ResolveLiveModel finds a model by provider id + model id in the static
// catalog first, then in the cached live list — never touching the network.
// It backs runtime route changes, which must not block on a fetch.
func ResolveLiveModel(project, providerID, modelID string) (Model, Provider, bool) {
	pr, ok := ProviderByID(providerID)
	if !ok {
		return Model{}, Provider{}, false
	}
	if m, ok := Lookup(pr.BaseURL, modelID); ok {
		return m, pr, true
	}
	if c := readLiveCache(project); c != nil {
		for _, m := range c.Models {
			if m.Provider == providerID && m.ID == modelID {
				return m, pr, true
			}
		}
	}
	return Model{}, Provider{}, false
}

// readLiveCache loads the cache file, returning nil when absent or corrupt.
func readLiveCache(project string) *LiveCache {
	raw, err := os.ReadFile(LiveModelsPath(project))
	if err != nil {
		return nil
	}
	var c LiveCache
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil
	}
	return &c
}

// WriteLiveCache atomically persists the live cache.
func WriteLiveCache(project string, c *LiveCache) error {
	path := LiveModelsPath(project)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
