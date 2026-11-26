package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ericflo/eagent/internal/llm"
)

// Live model discovery: each provider's /models endpoint is queried on demand —
// when something asks for the live list (the web Config page, the new-session
// dialog, the settings connector for the phone's model pickers) — never on the
// way to starting a session, so a start never waits on the network. Results are
// cached on disk for LiveTTL (cloud providers) and LocalLiveTTL (registered
// local/custom servers): a fresh cache is served without any HTTP, and once it
// is older the next request refreshes it. A refresh that finds nothing is
// retried after liveRetry. Keys only ever travel to the provider's own base URL
// with the key variable that provider names.

// LiveTTL is how long a fetched cloud list is used before it is fetched again.
const LiveTTL = 6 * time.Hour

// LocalLiveTTL is how long a registered local/custom server's model list is
// cached before being refreshed on demand.
const LocalLiveTTL = 30 * time.Second

// LiveModelsPath is the on-disk cache of the last live fetch, per project.
func LiveModelsPath(project string) string {
	return filepath.Join(project, ".agents", "eagent", "live-models.json")
}

// LiveCache is the persisted form of a live fetch.
type LiveCache struct {
	FetchedAt      time.Time         `json:"fetched_at"`
	LocalFetchedAt time.Time         `json:"local_fetched_at,omitempty"`
	Models         []Model           `json:"models,omitempty"`
	Errors         map[string]string `json:"errors,omitempty"` // provider id -> why it contributed nothing
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
// the providers when the cache is missing or older than LiveTTL (or
// LocalLiveTTL for registered custom/local servers). It never runs at startup;
// only callers that explicitly ask trigger HTTP fetches.
func LiveModels(ctx context.Context, project string) LiveResult {
	liveMu.Lock()
	defer liveMu.Unlock()
	cache := readLiveCache(project)
	custom := CustomProviders(project)
	cloudFresh := cache != nil && time.Since(cache.FetchedAt) < LiveTTL
	if cloudFresh {
		if len(custom) == 0 || (!cache.LocalFetchedAt.IsZero() && time.Since(cache.LocalFetchedAt) < LocalLiveTTL) {
			return LiveResult{Models: cache.Models, FetchedAt: cache.FetchedAt, Errors: cache.Errors}
		}
		// Refresh only registered local/custom servers while keeping the fresh
		// cloud cache untouched.
		freshLocal := refreshProviders(context.WithoutCancel(ctx), project, custom)
		now := time.Now()
		customSet := map[string]bool{}
		for _, pr := range custom {
			customSet[pr.ID] = true
		}
		merged := make([]Model, 0, len(cache.Models)+len(freshLocal.Models))
		for _, m := range cache.Models {
			if !customSet[m.Provider] {
				merged = append(merged, m)
			} else if _, failed := freshLocal.Errors[m.Provider]; failed {
				// Keep previously discovered models if the local server is briefly offline.
				merged = append(merged, m)
			}
		}
		merged = append(merged, freshLocal.Models...)
		sortLive(project, merged)
		errs := map[string]string{}
		for k, v := range cache.Errors {
			if !customSet[k] {
				errs[k] = v
			}
		}
		for k, v := range freshLocal.Errors {
			errs[k] = v
		}
		next := &LiveCache{FetchedAt: cache.FetchedAt, LocalFetchedAt: now, Models: merged, Errors: errs}
		_ = WriteLiveCache(project, next)
		return LiveResult{Models: merged, FetchedAt: cache.FetchedAt, Refreshed: true, Errors: errs}
	}

	// The refresh outlives the request that asked for it: a page closed
	// mid-fetch must not leave a cache of nothing behind.
	all := AllProviders(project)
	fresh := refreshProviders(context.WithoutCancel(ctx), project, all)
	now := time.Now()
	if cache == nil {
		cache = &LiveCache{}
	}
	if len(fresh.Models) > 0 {
		// A provider that failed this time keeps the models it listed
		// before, so one timeout does not hide its live-only models.
		for _, m := range cache.Models {
			if _, failed := fresh.Errors[m.Provider]; failed {
				fresh.Models = append(fresh.Models, m)
			}
		}
		sortLive(project, fresh.Models)
		fresh.FetchedAt = now
		fresh.Refreshed = true
		_ = WriteLiveCache(project, &LiveCache{FetchedAt: now, LocalFetchedAt: now, Models: fresh.Models, Errors: fresh.Errors})
		return fresh
	}
	// The refresh found nothing (offline, no keys). Serve whatever was cached
	// and try again in a while rather than on every request or a day later.
	cache.FetchedAt = now.Add(liveRetry - LiveTTL)
	cache.LocalFetchedAt = now
	cache.Errors = fresh.Errors
	_ = WriteLiveCache(project, cache)
	return LiveResult{Models: cache.Models, FetchedAt: cache.FetchedAt, Stale: cache.Models != nil, Errors: fresh.Errors}
}

// UpdateLiveCacheForServer replaces one registered server's models and error in
// live-models.json without perturbing cloud provider cache freshness.
func UpdateLiveCacheForServer(project, serverID string, models []Model, errMsg string) {
	liveMu.Lock()
	defer liveMu.Unlock()
	cache := readLiveCache(project)
	if cache == nil {
		cache = &LiveCache{}
	}
	kept := make([]Model, 0, len(cache.Models)+len(models))
	for _, m := range cache.Models {
		if m.Provider != serverID {
			kept = append(kept, m)
		}
	}
	kept = append(kept, models...)
	sortLive(project, kept)
	if cache.Errors == nil {
		cache.Errors = map[string]string{}
	}
	if errMsg != "" {
		cache.Errors[serverID] = errMsg
	} else {
		delete(cache.Errors, serverID)
	}
	cache.Models = kept
	cache.LocalFetchedAt = time.Now()
	_ = WriteLiveCache(project, cache)
}

// RemoveLiveCacheForServer drops a deleted server's cached models and error.
func RemoveLiveCacheForServer(project, serverID string) {
	liveMu.Lock()
	defer liveMu.Unlock()
	cache := readLiveCache(project)
	if cache == nil {
		return
	}
	kept := make([]Model, 0, len(cache.Models))
	for _, m := range cache.Models {
		if m.Provider != serverID {
			kept = append(kept, m)
		}
	}
	cache.Models = kept
	delete(cache.Errors, serverID)
	_ = WriteLiveCache(project, cache)
}

// SetLiveProbe replaces how providers are asked for their models and
// returns the previous probe. Tests outside this package use it so they
// never reach real providers with the developer's keys.
func SetLiveProbe(fn func(context.Context, Provider) ([]Model, error)) func(context.Context, Provider) ([]Model, error) {
	liveMu.Lock()
	defer liveMu.Unlock()
	old := liveProbe
	liveProbe = fn
	return old
}

// liveRetry is how soon a refresh that found nothing is tried again.
const liveRetry = 10 * time.Minute

// sortLive orders live models by provider display order, then id.
func sortLive(project string, models []Model) {
	order := map[string]int{}
	for i, pr := range AllProviders(project) {
		order[pr.ID] = i
	}
	sort.SliceStable(models, func(i, j int) bool {
		oi, oki := order[models[i].Provider]
		if !oki {
			oi = len(order)
		}
		oj, okj := order[models[j].Provider]
		if !okj {
			oj = len(order)
		}
		if oi != oj {
			return oi < oj
		}
		return models[i].ID < models[j].ID
	})
}

// refreshProviders probes every given provider concurrently and merges what
// came back, in provider order.
func refreshProviders(ctx context.Context, project string, providers []Provider) LiveResult {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	type out struct {
		pr     Provider
		models []Model
		err    error
	}
	ch := make(chan out, len(providers))
	probe := liveProbe // read under liveMu, which LiveModels holds
	for _, pr := range providers {
		go func(pr Provider) {
			ms, err := probe(ctx, pr)
			ch <- out{pr, ms, err}
		}(pr)
	}
	res := LiveResult{Errors: map[string]string{}}
	for range providers {
		o := <-ch
		if o.err != nil {
			res.Errors[o.pr.ID] = o.err.Error()
			continue
		}
		res.Models = append(res.Models, o.models...)
	}
	sortLive(project, res.Models)
	return res
}

// probeProviderHTTP fetches one provider's /models list. Providers whose
// endpoint needs a key are skipped (not an error worth surfacing) when the
// key variable is unset; OpenRouter's endpoint and keyless local servers need
// no key.
func probeProviderHTTP(ctx context.Context, pr Provider) ([]Model, error) {
	if pr.Custom {
		return probeCustomProviderHTTP(ctx, pr)
	}
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
		return url, "Authorization", nil, pr.KeyEnv == ""
	}
}

// rawLiveModel is the union of the fields cloud and local /models endpoints
// return (OpenRouter, Loom, LM Studio, vLLM, SGLang, llama.cpp, Ollama, etc.).
type rawLiveModel struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	DisplayName         string `json:"display_name"`
	OwnedBy             string `json:"owned_by"`
	Type                string `json:"type"`
	State               string `json:"state"`
	Arch                string `json:"arch"`
	Quantization        string `json:"quantization"`
	ContextLength       int64  `json:"context_length"`
	MaxModelLen         int64  `json:"max_model_len"`
	MaxContextLength    int64  `json:"max_context_length"`
	LoadedContextLength int64  `json:"loaded_context_length"`
	Meta                *struct {
		NCtxTrain int64 `json:"n_ctx_train"`
	} `json:"meta"`
	Limits *struct {
		MaxOutputTokens int64 `json:"max_output_tokens"`
	} `json:"limits"`
	Pricing *struct {
		Prompt         string `json:"prompt"`
		Completion     string `json:"completion"`
		InputCacheRead string `json:"input_cache_read"`
	} `json:"pricing"`
	Architecture *struct {
		InputModalities []string `json:"input_modalities"`
	} `json:"architecture"`
	InputModalities []string `json:"input_modalities"`
	// Capabilities is []string on LM Studio or an object on some servers.
	Capabilities json.RawMessage `json:"capabilities"`
	// Reasoning is an object on OpenRouter ({"mandatory":..., "supported_efforts":[...]})
	// and a boolean on Loom ("reasoning": true).
	Reasoning                json.RawMessage `json:"reasoning"`
	SupportedReasoningLevels []string        `json:"supported_reasoning_levels"`
	DefaultReasoningLevel    string          `json:"default_reasoning_level"`
	Loom                     *struct {
		Kind     string `json:"kind"`
		Endpoint string `json:"endpoint"`
	} `json:"loom"`
}

func decodeRawModels(body []byte, allowEmpty bool) ([]rawLiveModel, error) {
	var raws []json.RawMessage
	var env struct {
		Object string            `json:"object"`
		Data   []json.RawMessage `json:"data"`
		Error  json.RawMessage   `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err == nil {
		if len(env.Error) > 0 && string(env.Error) != "null" && env.Data == nil {
			return nil, fmt.Errorf("error response: %s", strings.TrimSpace(string(env.Error)))
		}
		if env.Data != nil {
			if len(env.Data) == 0 && !allowEmpty {
				return nil, errors.New("unrecognized models response (no data array)")
			}
			raws = env.Data
		}
	}
	if raws == nil && env.Data == nil {
		if err := json.Unmarshal(body, &raws); err != nil || (!allowEmpty && len(raws) == 0) {
			return nil, errors.New("unrecognized models response (no data array)")
		}
	}
	out := make([]rawLiveModel, 0, len(raws))
	for _, raw := range raws {
		var rm rawLiveModel
		if err := json.Unmarshal(raw, &rm); err != nil || rm.ID == "" {
			continue
		}
		out = append(out, rm)
	}
	if len(out) == 0 && !(allowEmpty && len(raws) == 0) {
		return nil, errors.New("no model ids in response")
	}
	return out, nil
}

// parseLiveModels decodes a /models response body (either {"data": [...]} or
// a bare array) into catalog Models for that provider. Models already in the
// static catalog keep their static metadata (price, protocol, efforts) and
// are just marked live; unknown ids become live entries the editor can still
// pick.
func parseLiveModels(pr Provider, body []byte) ([]Model, error) {
	rms, err := decodeRawModels(body, false)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []Model
	for _, rm := range rms {
		if seen[rm.ID] || isNonGenerativeModel(rm) {
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
	if !pr.Custom {
		if m, ok := Lookup(pr.BaseURL, rm.ID); ok {
			m.Live = true
			return m
		}
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

	// Context window: loaded_context_length > context_length > max_context_length > max_model_len > meta.n_ctx_train.
	for _, c := range []int64{rm.LoadedContextLength, rm.ContextLength, rm.MaxContextLength, rm.MaxModelLen} {
		if c > 0 {
			m.Context = int(c)
			break
		}
	}
	if m.Context == 0 && rm.Meta != nil && rm.Meta.NCtxTrain > 0 {
		m.Context = int(rm.Meta.NCtxTrain)
	}
	if rm.Limits != nil && rm.Limits.MaxOutputTokens > 0 {
		m.MaxOutput = int(rm.Limits.MaxOutputTokens)
	}

	// Vision / multimodal detection across OpenRouter, Loom, LM Studio, etc.
	if rm.Architecture != nil {
		for _, mod := range rm.Architecture.InputModalities {
			if mod == "image" || mod == "video" {
				m.Vision = true
			}
		}
	}
	for _, mod := range rm.InputModalities {
		if mod == "image" || mod == "video" {
			m.Vision = true
		}
	}
	if strings.EqualFold(rm.Type, "vlm") {
		m.Vision = true
	}
	if len(rm.Capabilities) > 0 {
		var capList []string
		if json.Unmarshal(rm.Capabilities, &capList) == nil {
			for _, capName := range capList {
				low := strings.ToLower(capName)
				if low == "vision" || low == "image" || low == "multimodal" {
					m.Vision = true
				}
			}
		} else {
			var capMap map[string]any
			if json.Unmarshal(rm.Capabilities, &capMap) == nil {
				if v, _ := capMap["vision"].(bool); v {
					m.Vision = true
				}
			}
		}
	}

	// Reasoning effort levels: OpenRouter object or Loom supported_reasoning_levels.
	var efforts []string
	if len(rm.Reasoning) > 0 {
		var rObj struct {
			Mandatory        bool     `json:"mandatory"`
			SupportedEfforts []string `json:"supported_efforts"`
		}
		if json.Unmarshal(rm.Reasoning, &rObj) == nil {
			efforts = append(efforts, rObj.SupportedEfforts...)
		}
	}
	efforts = append(efforts, rm.SupportedReasoningLevels...)
	for _, e := range efforts {
		if EffortIndex(e) < len(EffortOrder) && !slices.Contains(m.Efforts, e) {
			m.Efforts = append(m.Efforts, e)
		}
	}
	if len(m.Efforts) > 0 {
		sort.Slice(m.Efforts, func(i, j int) bool { return EffortIndex(m.Efforts[i]) < EffortIndex(m.Efforts[j]) })
	}

	if p := declaredModelProtocol(rm); p != "" {
		m.Protocol = p
	}

	if pr.ID == "openrouter" {
		// The Responses-needing OpenAI models are the ones the static catalog
		// marks; for unknown openai/* ids assume the same.
		if strings.HasPrefix(rm.ID, "openai/") {
			m.Protocol = llm.ProtocolResponses
		}
		if rm.Pricing != nil {
			if p, ok := livePrice(rm.Pricing); ok {
				m.Price = p
			}
		}
	}

	if pr.Custom {
		var notes []string
		if rm.State != "" && rm.State != "loaded" {
			notes = append(notes, rm.State)
		}
		if rm.Arch != "" {
			notes = append(notes, rm.Arch)
		}
		if rm.Quantization != "" {
			notes = append(notes, rm.Quantization)
		}
		if rm.DefaultReasoningLevel != "" {
			notes = append(notes, "default effort "+rm.DefaultReasoningLevel)
		}
		if len(notes) > 0 {
			m.Note = strings.Join(notes, " · ")
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
	// Routers such as openrouter/auto list "-1": the price depends on the
	// model they pick, so it is unknown, not negative.
	if in == 0 && out == 0 || in < 0 || out < 0 || cached < 0 {
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

// MergeLiveFor overlays live results on the static catalog for display,
// placing registered custom/local servers' live models first so they are
// immediately visible and never buried behind large cloud lists.
func MergeLiveFor(project string, static, live []Model) []Model {
	customIDs := map[string]bool{}
	for _, pr := range CustomProviders(project) {
		customIDs[pr.ID] = true
	}
	if len(customIDs) == 0 {
		return MergeLive(static, live)
	}
	var custom, cloud []Model
	for _, m := range live {
		if customIDs[m.Provider] {
			custom = append(custom, m)
		} else {
			cloud = append(cloud, m)
		}
	}
	return append(custom, MergeLive(static, cloud)...)
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

// ResolveLiveModel finds a model by provider id + model id in the static
// catalog first, then in the cached live list — never touching the network.
// It backs runtime route changes, which must not block on a fetch. For a
// registered custom server that has no cached models (e.g. a proxy without
// /models), any non-empty model id is accepted using the server's defaults.
func ResolveLiveModel(project, providerID, modelID string) (Model, Provider, bool) {
	pr, ok := ProviderByIDFor(project, providerID)
	if !ok {
		return Model{}, Provider{}, false
	}
	if !pr.Custom {
		if m, ok := Lookup(pr.BaseURL, modelID); ok {
			return m, pr, true
		}
	}
	hasAnyForProvider := false
	if c := readLiveCache(project); c != nil {
		for _, m := range c.Models {
			if m.Provider == providerID {
				hasAnyForProvider = true
				if m.ID == modelID {
					return m, pr, true
				}
			}
		}
	}
	if pr.Custom && !hasAnyForProvider && strings.TrimSpace(modelID) != "" {
		return Model{
			Provider:  pr.ID,
			ID:        modelID,
			Family:    familyOf(modelID),
			Label:     modelID,
			Protocol:  pr.Protocol,
			Protocols: slices.Clone(pr.Protocols),
		}, pr, true
	}
	return Model{}, Provider{}, false
}

// readLiveCache loads the cache file, returning nil when absent or corrupt.
func readLiveCache(project string) *LiveCache {
	path := LiveModelsPath(project)
	st, err := os.Stat(path)
	if err != nil {
		return nil
	}
	// The file is read on every settings publication; parse it again only
	// when it changed.
	cacheMemo.Lock()
	defer cacheMemo.Unlock()
	if m, ok := cacheMemo.byPath[path]; ok && m.size == st.Size() && m.mod.Equal(st.ModTime()) {
		return m.cache.clone()
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var c LiveCache
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil
	}
	cacheMemo.byPath[path] = memoEntry{size: st.Size(), mod: st.ModTime(), cache: &c}
	return c.clone()
}

// clone is a copy that shares no slice or map with c. The memoized cache
// is read by callers that encode it outside any lock while others edit
// their copy in place, so each gets its own.
func (c *LiveCache) clone() *LiveCache {
	out := *c
	out.Models = slices.Clone(c.Models)
	out.Errors = maps.Clone(c.Errors)
	return &out
}

type memoEntry struct {
	size  int64
	mod   time.Time
	cache *LiveCache
}

var cacheMemo = struct {
	sync.Mutex
	byPath map[string]memoEntry
}{byPath: map[string]memoEntry{}}

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
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	cacheMemo.Lock()
	delete(cacheMemo.byPath, path)
	cacheMemo.Unlock()
	return nil
}
