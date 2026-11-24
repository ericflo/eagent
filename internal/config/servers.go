package config

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ericflo/eagent/internal/boundedfile"
	"github.com/ericflo/eagent/internal/llm"
)

// RegisteredServer is a user-registered local or custom model server stored
// per project in .agents/eagent/servers.json. Once registered, it behaves as a
// first-class provider across the CLI, web UI, runtime model switching, and
// third-party UIs such as FinaleChat.
type RegisteredServer struct {
	ID        string    `json:"id"`
	Label     string    `json:"label"`
	BaseURL   string    `json:"base_url"`
	Protocol  string    `json:"protocol"`            // default wire protocol: openai-chat | openai-responses | anthropic
	Protocols []string  `json:"protocols,omitempty"` // wire protocols confirmed supported during probing
	KeyEnv    string    `json:"key_env,omitempty"`   // optional env var name for auth; "" means no auth
	Engine    string    `json:"engine,omitempty"`    // detected server kind (lmstudio, loom, llamacpp, vllm, sglang, ollama, ...)
	Note      string    `json:"note,omitempty"`
	ProbedAt  time.Time `json:"probed_at,omitempty"`
}

// Provider converts a registered server into the catalog Provider shape.
func (s RegisteredServer) Provider() Provider {
	proto := s.Protocol
	if proto == "" {
		proto = llm.ProtocolChat
	}
	return Provider{
		ID:        s.ID,
		Label:     s.Label,
		BaseURL:   strings.TrimRight(s.BaseURL, "/"),
		Protocol:  proto,
		Protocols: slices.Clone(s.Protocols),
		KeyEnv:    s.KeyEnv,
		Note:      s.Note,
		Custom:    true,
		Engine:    s.Engine,
	}
}

// ProbeOptions controls probing or registering a model server URL.
type ProbeOptions struct {
	URL      string `json:"url"`
	Protocol string `json:"protocol,omitempty"`
	KeyEnv   string `json:"key_env,omitempty"`
	ID       string `json:"id,omitempty"`
	Label    string `json:"label,omitempty"`
	Note     string `json:"note,omitempty"`
}

// ProbeResult is the outcome of inspecting a model server's /models and API
// endpoints.
type ProbeResult struct {
	Server   RegisteredServer `json:"server"`
	Models   []Model          `json:"models"`
	Warnings []string         `json:"warnings,omitempty"`
}

// ServersPath is where a project stores its registered local/custom servers.
func ServersPath(project string) string {
	return filepath.Join(project, ".agents", "eagent", "servers.json")
}

type serversFile struct {
	Servers []RegisteredServer `json:"servers"`
}

type serversMemoEntry struct {
	size    int64
	mod     time.Time
	servers []RegisteredServer
}

var serversMemo = struct {
	sync.Mutex
	byPath map[string]serversMemoEntry
}{byPath: map[string]serversMemoEntry{}}

// LoadServers reads a project's registered servers. Missing file returns nil, nil.
func LoadServers(project string) ([]RegisteredServer, error) {
	if project == "" {
		return nil, nil
	}
	path := ServersPath(project)
	st, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	serversMemo.Lock()
	if m, ok := serversMemo.byPath[path]; ok && m.size == st.Size() && m.mod.Equal(st.ModTime()) {
		out := cloneServers(m.servers)
		serversMemo.Unlock()
		return out, nil
	}
	serversMemo.Unlock()

	raw, err := boundedfile.Read(path, 1<<20)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var list []RegisteredServer
	var wrap serversFile
	if err := json.Unmarshal(raw, &wrap); err == nil && wrap.Servers != nil {
		list = wrap.Servers
	} else if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	clean := make([]RegisteredServer, 0, len(list))
	seen := map[string]bool{}
	for _, s := range list {
		s.ID = strings.TrimSpace(s.ID)
		s.BaseURL = strings.TrimRight(strings.TrimSpace(s.BaseURL), "/")
		if s.ID == "" || s.BaseURL == "" || seen[s.ID] {
			continue
		}
		if s.Label == "" {
			s.Label = s.ID
		}
		if s.Protocol == "" {
			s.Protocol = llm.ProtocolChat
		}
		seen[s.ID] = true
		clean = append(clean, s)
	}
	serversMemo.Lock()
	serversMemo.byPath[path] = serversMemoEntry{size: st.Size(), mod: st.ModTime(), servers: cloneServers(clean)}
	serversMemo.Unlock()
	return clean, nil
}

func cloneServers(in []RegisteredServer) []RegisteredServer {
	out := make([]RegisteredServer, len(in))
	for i, s := range in {
		s.Protocols = slices.Clone(s.Protocols)
		out[i] = s
	}
	return out
}

func writeServersLocked(project string, servers []RegisteredServer) error {
	path := ServersPath(project)
	prev, err := boundedfile.Read(path, 1<<20)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if servers == nil {
		servers = []RegisteredServer{}
	}
	raw, err := json.MarshalIndent(serversFile{Servers: servers}, "", "  ")
	if err != nil {
		return err
	}
	if err := writeAtomic(path, append(raw, '\n'), prev); err != nil {
		return err
	}
	serversMemo.Lock()
	delete(serversMemo.byPath, path)
	serversMemo.Unlock()
	return nil
}

// CustomProviders returns a project's registered servers as catalog Providers.
func CustomProviders(project string) []Provider {
	servers, err := LoadServers(project)
	if err != nil || len(servers) == 0 {
		return nil
	}
	out := make([]Provider, 0, len(servers))
	for _, s := range servers {
		out = append(out, s.Provider())
	}
	return out
}

// AllProviders returns the project's registered custom/local servers followed
// by the built-in catalog providers.
func AllProviders(project string) []Provider {
	custom := CustomProviders(project)
	if len(custom) == 0 {
		out := make([]Provider, len(Providers))
		copy(out, Providers)
		return out
	}
	out := make([]Provider, 0, len(custom)+len(Providers))
	out = append(out, custom...)
	out = append(out, Providers...)
	return out
}

// ProviderByIDFor looks up a provider by id across the project's registered
// servers and the built-in catalog.
func ProviderByIDFor(project, id string) (Provider, bool) {
	if pr, ok := ProviderByID(id); ok {
		return pr, true
	}
	for _, pr := range CustomProviders(project) {
		if pr.ID == id {
			return pr, true
		}
	}
	return Provider{}, false
}

// ProviderForURLFor finds the provider serving baseURL across the built-in
// catalog and the project's registered servers.
func ProviderForURLFor(project, baseURL string) (Provider, bool) {
	if pr, ok := ProviderForURL(baseURL); ok {
		return pr, true
	}
	u := strings.TrimRight(baseURL, "/")
	for _, pr := range CustomProviders(project) {
		if strings.TrimRight(pr.BaseURL, "/") == u {
			return pr, true
		}
	}
	return Provider{}, false
}

// KnownBaseURLFor reports whether baseURL belongs to a built-in provider or a
// registered server in project.
func KnownBaseURLFor(project, baseURL string) bool {
	_, ok := ProviderForURLFor(project, baseURL)
	return ok
}

// KnownKeyEnvFor reports whether name is used by a built-in provider or a
// registered server in project.
func KnownKeyEnvFor(project, name string) bool {
	if name == "" {
		return false
	}
	if KnownKeyEnv(name) {
		return true
	}
	for _, pr := range CustomProviders(project) {
		if pr.KeyEnv == name {
			return true
		}
	}
	return false
}

// KeyEnvsFor returns every key environment variable named by the built-in
// catalog or the project's registered servers, sorted.
func KeyEnvsFor(project string) []string {
	seen := map[string]bool{}
	var out []string
	for _, k := range KeyEnvs() {
		seen[k] = true
		out = append(out, k)
	}
	for _, pr := range CustomProviders(project) {
		if pr.KeyEnv != "" && !seen[pr.KeyEnv] {
			seen[pr.KeyEnv] = true
			out = append(out, pr.KeyEnv)
		}
	}
	sort.Strings(out)
	return out
}

var serverIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,47}$`)

// ValidServerID reports whether id is a valid custom server identifier that
// does not collide with any built-in catalog provider.
func ValidServerID(id string) bool {
	if !serverIDPattern.MatchString(id) {
		return false
	}
	_, builtin := ProviderByID(id)
	return !builtin
}

func validProtocol(p string) bool {
	switch p {
	case llm.ProtocolChat, llm.ProtocolResponses, llm.ProtocolAnthropic:
		return true
	default:
		return false
	}
}

// NormalizeServerURL cleans any user-typed server address (with or without
// scheme or /v1, or even a full endpoint path like /v1/chat/completions or
// /v1/messages) into ordered candidate base URLs and an optional protocol hint
// inferred from a pasted endpoint path.
func NormalizeServerURL(raw string) (candidates []string, hintProtocol string, err error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil, "", errors.New("server URL is required")
	}
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, "", fmt.Errorf("invalid server URL %q (expected http:// or https:// host)", raw)
	}
	u.RawQuery = ""
	u.Fragment = ""
	path := strings.TrimRight(u.Path, "/")
	for _, suffix := range []struct {
		tail  string
		proto string
	}{
		{"/chat/completions", llm.ProtocolChat},
		{"/responses", llm.ProtocolResponses},
		{"/messages", llm.ProtocolAnthropic},
		{"/models", ""},
	} {
		if strings.HasSuffix(path, suffix.tail) {
			path = strings.TrimRight(strings.TrimSuffix(path, suffix.tail), "/")
			if suffix.proto != "" {
				hintProtocol = suffix.proto
			}
			break
		}
	}
	u.Path = path
	cleaned := strings.TrimRight(u.String(), "/")
	add := func(c string) {
		c = strings.TrimRight(c, "/")
		if c != "" && !slices.Contains(candidates, c) {
			candidates = append(candidates, c)
		}
	}
	switch {
	case path == "":
		add(cleaned + "/v1")
		add(cleaned)
	case strings.HasSuffix(path, "/v1") || strings.HasSuffix(path, "/api/v1") || strings.HasSuffix(path, "/v1/openai"):
		add(cleaned)
		if strings.HasSuffix(path, "/v1") {
			add(strings.TrimSuffix(cleaned, "/v1"))
		}
	default:
		add(cleaned)
		add(cleaned + "/v1")
	}
	return candidates, hintProtocol, nil
}

var probeHTTPClient = &http.Client{Timeout: 6 * time.Second}

// ProbeServer connects to any user-supplied model server URL, queries its
// /models endpoint (plus optional server metadata such as LM Studio's
// /api/v0/models or llama.cpp's /props), and probes which wire protocols
// (openai-chat, openai-responses, anthropic) the server accepts without
// generating any tokens.
func ProbeServer(ctx context.Context, opts ProbeOptions) (ProbeResult, error) {
	opts.Protocol = strings.TrimSpace(opts.Protocol)
	opts.KeyEnv = strings.TrimSpace(opts.KeyEnv)
	opts.ID = strings.TrimSpace(opts.ID)
	opts.Label = strings.TrimSpace(opts.Label)
	opts.Note = strings.TrimSpace(opts.Note)

	if opts.Protocol != "" && !validProtocol(opts.Protocol) {
		return ProbeResult{}, fmt.Errorf("unknown protocol %q (use %s, %s, or %s)", opts.Protocol, llm.ProtocolChat, llm.ProtocolResponses, llm.ProtocolAnthropic)
	}
	if opts.KeyEnv != "" && !keyEnvPattern.MatchString(opts.KeyEnv) {
		return ProbeResult{}, fmt.Errorf("key_env %q must look like an environment variable name", opts.KeyEnv)
	}
	if opts.ID != "" && !ValidServerID(opts.ID) {
		return ProbeResult{}, fmt.Errorf("server id %q must use lowercase letters, digits, '-' and '_' only (and cannot reuse a built-in provider id)", opts.ID)
	}
	candidates, hintProto, err := NormalizeServerURL(opts.URL)
	if err != nil {
		return ProbeResult{}, err
	}

	var warnings []string
	key := ""
	if opts.KeyEnv != "" {
		key = os.Getenv(opts.KeyEnv)
		if key == "" {
			warnings = append(warnings, fmt.Sprintf("$%s is not set in this shell", opts.KeyEnv))
		}
	}

	probeCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()

	// Step 1: Discover /models across candidate base URLs.
	var (
		resolvedBase  string
		rawModels     []rawLiveModel
		modelsFound   bool
		serverHeader  string
		authErr       error
		connectErr    error
		lastModelsErr error
	)
	for _, base := range candidates {
		rms, hdr, status, err := fetchRawModels(probeCtx, base, key, opts.Protocol == llm.ProtocolAnthropic || hintProto == llm.ProtocolAnthropic)
		if hdr != "" && serverHeader == "" {
			serverHeader = hdr
		}
		if err == nil {
			resolvedBase = base
			rawModels = rms
			modelsFound = true
			break
		}
		if status == http.StatusUnauthorized || status == http.StatusForbidden {
			authErr = err
			continue
		}
		if status == 0 {
			connectErr = err
			break // host unreachable; other paths on the same host will fail identically
		}
		lastModelsErr = err
	}
	if !modelsFound {
		if connectErr != nil {
			return ProbeResult{}, fmt.Errorf("cannot reach %s: %w", candidates[0], connectErr)
		}
		if authErr != nil {
			if opts.KeyEnv == "" {
				return ProbeResult{}, fmt.Errorf("%s requires authentication (%v); set an API key environment variable", candidates[0], authErr)
			}
			return ProbeResult{}, fmt.Errorf("%s rejected authentication (%v); check $%s", candidates[0], authErr, opts.KeyEnv)
		}
		// Some proxies/servers omit /models; still probe API endpoints below.
		resolvedBase = candidates[0]
		if lastModelsErr != nil {
			warnings = append(warnings, fmt.Sprintf("could not list /models (%v); probed API endpoints directly", lastModelsErr))
		}
	}

	// Step 2: Best-effort server metadata enrichment (/api/v0/models for LM Studio,
	// /props for llama.cpp) and engine detection.
	origin := originOf(resolvedBase)
	enrich := fetchServerEnrichment(probeCtx, origin, key)
	engine := detectEngine(rawModels, enrich, serverHeader)

	// Step 3: Probe the three wire protocols concurrently (no tokens generated).
	// Prefer a loaded generative model when enrichment knows model states/types.
	probeModel := ""
	for _, rm := range rawModels {
		if rm.ID == "" {
			continue
		}
		if lms, ok := enrich.lmStudioByID[rm.ID]; ok {
			if strings.EqualFold(lms.Type, "embeddings") || strings.EqualFold(lms.Type, "embedding") {
				continue
			}
			if strings.EqualFold(lms.State, "loaded") {
				probeModel = rm.ID
				break
			}
		}
	}
	if probeModel == "" {
		for _, rm := range rawModels {
			if rm.ID == "" || isNonGenerativeModel(rm) {
				continue
			}
			if lms, ok := enrich.lmStudioByID[rm.ID]; ok && (strings.EqualFold(lms.Type, "embeddings") || strings.EqualFold(lms.Type, "embedding")) {
				continue
			}
			probeModel = rm.ID
			break
		}
	}
	if probeModel == "" && len(rawModels) > 0 {
		probeModel = rawModels[0].ID
	}
	detectedProtos := probeAPIEndpoints(probeCtx, resolvedBase, probeModel, key)

	// Collect any endpoints explicitly declared by models (e.g. Loom).
	for _, rm := range rawModels {
		if p := declaredModelProtocol(rm); p != "" && !slices.Contains(detectedProtos, p) {
			detectedProtos = append(detectedProtos, p)
		}
	}
	detectedProtos = orderProtocols(detectedProtos)

	if !modelsFound && len(detectedProtos) == 0 {
		return ProbeResult{}, fmt.Errorf("no compatible /models or API endpoints found at %s", resolvedBase)
	}

	// Choose the server's default protocol.
	chosenProto := opts.Protocol
	if chosenProto != "" {
		if len(detectedProtos) > 0 && !slices.Contains(detectedProtos, chosenProto) {
			warnings = append(warnings, fmt.Sprintf("requested protocol %s did not respond to probe (detected: %s)", chosenProto, strings.Join(detectedProtos, ", ")))
		}
	} else if hintProto != "" && (len(detectedProtos) == 0 || slices.Contains(detectedProtos, hintProto)) {
		chosenProto = hintProto
	} else if len(detectedProtos) > 0 {
		chosenProto = detectedProtos[0] // canonical order prefers openai-chat, then openai-responses, then anthropic
	} else {
		chosenProto = llm.ProtocolChat
		warnings = append(warnings, "could not verify API protocol automatically; defaulting to openai-chat")
	}

	id := opts.ID
	if id == "" {
		id = suggestServerID(engine, resolvedBase)
	}
	label := opts.Label
	if label == "" {
		label = suggestServerLabel(engine, resolvedBase)
	}

	srv := RegisteredServer{
		ID:        id,
		Label:     label,
		BaseURL:   resolvedBase,
		Protocol:  chosenProto,
		Protocols: detectedProtos,
		KeyEnv:    opts.KeyEnv,
		Engine:    engine,
		Note:      opts.Note,
		ProbedAt:  time.Now().UTC().Truncate(time.Second),
	}
	pr := srv.Provider()
	models := buildCustomModels(pr, rawModels, enrich)
	if modelsFound && len(models) == 0 {
		warnings = append(warnings, "server is reachable, but no generative models are currently loaded")
	}
	return ProbeResult{Server: srv, Models: models, Warnings: warnings}, nil
}

// RegisterServer probes (unless skipProbe is true) and registers a new local or
// custom server in .agents/eagent/servers.json, updating live-models.json.
func RegisterServer(ctx context.Context, project string, opts ProbeOptions, skipProbe bool) (ProbeResult, error) {
	var res ProbeResult
	var err error
	if skipProbe {
		res, err = buildUnprobedServer(opts)
	} else {
		res, err = ProbeServer(ctx, opts)
	}
	if err != nil {
		return ProbeResult{}, err
	}

	editor, err := LockEditor(ctx, project)
	if err != nil {
		return ProbeResult{}, err
	}
	defer editor.Close()

	existing, err := LoadServers(project)
	if err != nil {
		return ProbeResult{}, err
	}
	// If the caller didn't force an ID and this exact base URL is already
	// registered, update that entry rather than creating a duplicate.
	if opts.ID == "" {
		for i, s := range existing {
			if strings.TrimRight(s.BaseURL, "/") == strings.TrimRight(res.Server.BaseURL, "/") {
				res.Server.ID = s.ID
				if opts.Label == "" && s.Label != "" {
					res.Server.Label = s.Label
				}
				if opts.Note == "" && s.Note != "" {
					res.Server.Note = s.Note
				}
				for j := range res.Models {
					res.Models[j].Provider = s.ID
				}
				existing[i] = res.Server
				if err := writeServersLocked(project, existing); err != nil {
					return ProbeResult{}, err
				}
				if !skipProbe {
					UpdateLiveCacheForServer(project, res.Server.ID, res.Models, "")
				}
				return res, nil
			}
		}
		res.Server.ID = uniqueServerID(res.Server.ID, existing)
		for j := range res.Models {
			res.Models[j].Provider = res.Server.ID
		}
	} else {
		for _, s := range existing {
			if s.ID == res.Server.ID {
				return ProbeResult{}, fmt.Errorf("a server with id %q is already registered", res.Server.ID)
			}
		}
	}

	existing = append(existing, res.Server)
	if err := writeServersLocked(project, existing); err != nil {
		return ProbeResult{}, err
	}
	if !skipProbe {
		UpdateLiveCacheForServer(project, res.Server.ID, res.Models, "")
	}
	return res, nil
}

// UpdateServer updates an existing registered server (and re-probes unless
// skipProbe is true).
func UpdateServer(ctx context.Context, project, id string, opts ProbeOptions, skipProbe bool) (ProbeResult, error) {
	servers, err := LoadServers(project)
	if err != nil {
		return ProbeResult{}, err
	}
	idx := slices.IndexFunc(servers, func(s RegisteredServer) bool { return s.ID == id })
	if idx < 0 {
		return ProbeResult{}, fmt.Errorf("no registered server named %q", id)
	}
	cur := servers[idx]
	if strings.TrimSpace(opts.URL) == "" {
		opts.URL = cur.BaseURL
	}
	opts.ID = id
	if opts.Label == "" {
		opts.Label = cur.Label
	}
	var res ProbeResult
	if skipProbe {
		if opts.Protocol == "" {
			opts.Protocol = cur.Protocol
		}
		res, err = buildUnprobedServer(opts)
		if err != nil {
			return ProbeResult{}, err
		}
		if res.Server.Engine == "" {
			res.Server.Engine = cur.Engine
		}
		if len(res.Server.Protocols) == 0 && strings.TrimRight(res.Server.BaseURL, "/") == strings.TrimRight(cur.BaseURL, "/") {
			res.Server.Protocols = cur.Protocols
			res.Server.ProbedAt = cur.ProbedAt
		}
	} else {
		res, err = ProbeServer(ctx, opts)
		if err != nil {
			return ProbeResult{}, err
		}
	}

	editor, err := LockEditor(ctx, project)
	if err != nil {
		return ProbeResult{}, err
	}
	defer editor.Close()

	servers, err = LoadServers(project)
	if err != nil {
		return ProbeResult{}, err
	}
	idx = slices.IndexFunc(servers, func(s RegisteredServer) bool { return s.ID == id })
	if idx < 0 {
		return ProbeResult{}, fmt.Errorf("no registered server named %q", id)
	}
	servers[idx] = res.Server
	if err := writeServersLocked(project, servers); err != nil {
		return ProbeResult{}, err
	}
	if !skipProbe {
		UpdateLiveCacheForServer(project, id, res.Models, "")
	}
	return res, nil
}

// RefreshServer re-probes one registered server, updates its capabilities in
// servers.json, and updates its models in live-models.json.
func RefreshServer(ctx context.Context, project, id string) (ProbeResult, error) {
	servers, err := LoadServers(project)
	if err != nil {
		return ProbeResult{}, err
	}
	idx := slices.IndexFunc(servers, func(s RegisteredServer) bool { return s.ID == id })
	if idx < 0 {
		return ProbeResult{}, fmt.Errorf("no registered server named %q", id)
	}
	cur := servers[idx]
	res, err := ProbeServer(ctx, ProbeOptions{
		URL:      cur.BaseURL,
		Protocol: cur.Protocol,
		KeyEnv:   cur.KeyEnv,
		ID:       cur.ID,
		Label:    cur.Label,
		Note:     cur.Note,
	})
	if err != nil {
		UpdateLiveCacheForServer(project, id, nil, err.Error())
		return ProbeResult{}, err
	}
	editor, err := LockEditor(ctx, project)
	if err != nil {
		return ProbeResult{}, err
	}
	defer editor.Close()

	servers, err = LoadServers(project)
	if err != nil {
		return ProbeResult{}, err
	}
	idx = slices.IndexFunc(servers, func(s RegisteredServer) bool { return s.ID == id })
	if idx < 0 {
		return ProbeResult{}, fmt.Errorf("no registered server named %q", id)
	}
	servers[idx] = res.Server
	if err := writeServersLocked(project, servers); err != nil {
		return ProbeResult{}, err
	}
	UpdateLiveCacheForServer(project, id, res.Models, "")
	return res, nil
}

// RefreshAllServers re-probes every registered server concurrently.
func RefreshAllServers(ctx context.Context, project string) (map[string]ProbeResult, map[string]string, error) {
	servers, err := LoadServers(project)
	if err != nil {
		return nil, nil, err
	}
	results := map[string]ProbeResult{}
	errs := map[string]string{}
	if len(servers) == 0 {
		return results, errs, nil
	}
	type item struct {
		id  string
		res ProbeResult
		err error
	}
	ch := make(chan item, len(servers))
	for _, s := range servers {
		go func(s RegisteredServer) {
			r, e := ProbeServer(ctx, ProbeOptions{
				URL:      s.BaseURL,
				Protocol: s.Protocol,
				KeyEnv:   s.KeyEnv,
				ID:       s.ID,
				Label:    s.Label,
				Note:     s.Note,
			})
			ch <- item{id: s.ID, res: r, err: e}
		}(s)
	}
	for range servers {
		it := <-ch
		if it.err != nil {
			errs[it.id] = it.err.Error()
			UpdateLiveCacheForServer(project, it.id, nil, it.err.Error())
			continue
		}
		results[it.id] = it.res
		UpdateLiveCacheForServer(project, it.id, it.res.Models, "")
	}
	if len(results) > 0 {
		editor, err := LockEditor(ctx, project)
		if err != nil {
			return results, errs, err
		}
		defer editor.Close()
		cur, err := LoadServers(project)
		if err == nil {
			for i, s := range cur {
				if r, ok := results[s.ID]; ok {
					cur[i] = r.Server
				}
			}
			_ = writeServersLocked(project, cur)
		}
	}
	return results, errs, nil
}

// DeleteServer removes a registered server from servers.json and drops its
// entries from live-models.json.
func DeleteServer(ctx context.Context, project, id string) error {
	editor, err := LockEditor(ctx, project)
	if err != nil {
		return err
	}
	defer editor.Close()

	servers, err := LoadServers(project)
	if err != nil {
		return err
	}
	idx := slices.IndexFunc(servers, func(s RegisteredServer) bool { return s.ID == id })
	if idx < 0 {
		return fmt.Errorf("no registered server named %q", id)
	}
	servers = slices.Delete(servers, idx, idx+1)
	if err := writeServersLocked(project, servers); err != nil {
		return err
	}
	RemoveLiveCacheForServer(project, id)
	return nil
}

// SyncServerSpec describes one desired server entry in a declarative server
// list update (such as a reviewed remote FinaleChat edit).
type SyncServerSpec struct {
	ID        string `json:"id,omitempty"`
	URL       string `json:"url"`
	Label     string `json:"label,omitempty"`
	Protocol  string `json:"protocol,omitempty"`
	KeyEnv    string `json:"key_env,omitempty"`
	Note      string `json:"note,omitempty"`
	SkipProbe bool   `json:"skip_probe,omitempty"`
}

// SyncServers reconciles project's servers.json with specs: unchanged existing
// servers keep their cached capabilities (updating label/protocol/note without
// blocking on network), new or URL/auth-changed servers are probed (unless
// SkipProbe is set), and removed servers are dropped from servers.json and
// live-models.json.
func SyncServers(ctx context.Context, project string, specs []SyncServerSpec) ([]RegisteredServer, error) {
	existing, err := LoadServers(project)
	if err != nil {
		return nil, err
	}
	byID := map[string]RegisteredServer{}
	byURL := map[string]RegisteredServer{}
	for _, s := range existing {
		byID[s.ID] = s
		byURL[strings.TrimRight(s.BaseURL, "/")] = s
	}

	next := make([]RegisteredServer, 0, len(specs))
	probedModels := map[string][]Model{}
	usedIDs := map[string]bool{}

	for i, sp := range specs {
		opts := ProbeOptions{
			URL:      strings.TrimSpace(sp.URL),
			Protocol: strings.TrimSpace(sp.Protocol),
			KeyEnv:   strings.TrimSpace(sp.KeyEnv),
			ID:       strings.TrimSpace(sp.ID),
			Label:    strings.TrimSpace(sp.Label),
			Note:     strings.TrimSpace(sp.Note),
		}
		cands, hintProto, err := NormalizeServerURL(opts.URL)
		if err != nil {
			return nil, fmt.Errorf("server #%d: %w", i+1, err)
		}
		if opts.Protocol != "" && !validProtocol(opts.Protocol) {
			return nil, fmt.Errorf("server #%d: unknown protocol %q (use %s, %s, or %s)", i+1, opts.Protocol, llm.ProtocolChat, llm.ProtocolResponses, llm.ProtocolAnthropic)
		}
		if opts.KeyEnv != "" && !keyEnvPattern.MatchString(opts.KeyEnv) {
			return nil, fmt.Errorf("server #%d: key_env %q must look like an environment variable name", i+1, opts.KeyEnv)
		}
		if opts.ID != "" && !ValidServerID(opts.ID) {
			return nil, fmt.Errorf("server #%d: invalid server id %q", i+1, opts.ID)
		}

		// Match against an existing entry by ID or normalized base URL.
		var prev RegisteredServer
		matched := false
		if opts.ID != "" {
			prev, matched = byID[opts.ID]
		}
		if !matched {
			for _, c := range cands {
				if ex, ok := byURL[strings.TrimRight(c, "/")]; ok {
					prev, matched = ex, true
					break
				}
			}
		}

		sameEndpoint := matched && slices.Contains(cands, strings.TrimRight(prev.BaseURL, "/")) && prev.KeyEnv == opts.KeyEnv
		var srv RegisteredServer
		if sameEndpoint && (!prev.ProbedAt.IsZero() || sp.SkipProbe) {
			srv = prev
			if opts.Label != "" {
				srv.Label = opts.Label
			}
			if opts.Protocol != "" {
				srv.Protocol = opts.Protocol
			} else if hintProto != "" {
				srv.Protocol = hintProto
			}
			srv.Note = opts.Note
		} else if sp.SkipProbe {
			res, err := buildUnprobedServer(opts)
			if err != nil {
				return nil, fmt.Errorf("server #%d: %w", i+1, err)
			}
			srv = res.Server
		} else {
			res, err := ProbeServer(ctx, opts)
			if err != nil {
				return nil, fmt.Errorf("server %s: %w", opts.URL, err)
			}
			srv = res.Server
			probedModels[srv.ID] = res.Models
		}
		if opts.ID == "" && matched && !usedIDs[prev.ID] {
			oldID := srv.ID
			srv.ID = prev.ID
			if ms, ok := probedModels[oldID]; ok {
				delete(probedModels, oldID)
				for j := range ms {
					ms[j].Provider = srv.ID
				}
				probedModels[srv.ID] = ms
			}
		}
		if usedIDs[srv.ID] {
			if opts.ID != "" {
				return nil, fmt.Errorf("duplicate server id %q", srv.ID)
			}
			oldID := srv.ID
			srv.ID = uniqueServerID(srv.ID, next)
			if ms, ok := probedModels[oldID]; ok {
				delete(probedModels, oldID)
				for j := range ms {
					ms[j].Provider = srv.ID
				}
				probedModels[srv.ID] = ms
			}
		}
		usedIDs[srv.ID] = true
		next = append(next, srv)
	}

	editor, err := LockEditor(ctx, project)
	if err != nil {
		return nil, err
	}
	defer editor.Close()

	if err := writeServersLocked(project, next); err != nil {
		return nil, err
	}
	for _, old := range existing {
		if !usedIDs[old.ID] {
			RemoveLiveCacheForServer(project, old.ID)
		}
	}
	for id, ms := range probedModels {
		UpdateLiveCacheForServer(project, id, ms, "")
	}
	return next, nil
}

func buildUnprobedServer(opts ProbeOptions) (ProbeResult, error) {
	opts.Protocol = strings.TrimSpace(opts.Protocol)
	opts.KeyEnv = strings.TrimSpace(opts.KeyEnv)
	opts.ID = strings.TrimSpace(opts.ID)
	opts.Label = strings.TrimSpace(opts.Label)
	opts.Note = strings.TrimSpace(opts.Note)
	candidates, hintProto, err := NormalizeServerURL(opts.URL)
	if err != nil {
		return ProbeResult{}, err
	}
	if opts.Protocol == "" {
		if hintProto != "" {
			opts.Protocol = hintProto
		} else {
			opts.Protocol = llm.ProtocolChat
		}
	}
	if !validProtocol(opts.Protocol) {
		return ProbeResult{}, fmt.Errorf("unknown protocol %q", opts.Protocol)
	}
	if opts.KeyEnv != "" && !keyEnvPattern.MatchString(opts.KeyEnv) {
		return ProbeResult{}, fmt.Errorf("key_env %q must look like an environment variable name", opts.KeyEnv)
	}
	if opts.ID != "" && !ValidServerID(opts.ID) {
		return ProbeResult{}, fmt.Errorf("server id %q must use lowercase letters, digits, '-' and '_' only (and cannot reuse a built-in provider id)", opts.ID)
	}
	base := candidates[0]
	id := opts.ID
	if id == "" {
		id = suggestServerID("", base)
	}
	label := opts.Label
	if label == "" {
		label = suggestServerLabel("", base)
	}
	return ProbeResult{
		Server: RegisteredServer{
			ID:       id,
			Label:    label,
			BaseURL:  base,
			Protocol: opts.Protocol,
			KeyEnv:   opts.KeyEnv,
			Note:     opts.Note,
		},
	}, nil
}

// ---- HTTP probing helpers ----------------------------------------------------

func fetchRawModels(ctx context.Context, baseURL, key string, preferAnthropicAuth bool) ([]rawLiveModel, string, int, error) {
	endpoint := strings.TrimRight(baseURL, "/") + "/models"
	doFetch := func(anthropicAuth bool) (*http.Response, []byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, nil, err
		}
		req.Header.Set("Accept", "application/json")
		if key != "" {
			if anthropicAuth {
				req.Header.Set("x-api-key", key)
				req.Header.Set("anthropic-version", "2023-06-01")
			} else {
				req.Header.Set("Authorization", "Bearer "+key)
			}
		}
		resp, err := probeHTTPClient.Do(req)
		if err != nil {
			return nil, nil, err
		}
		defer resp.Body.Close()
		body, rerr := io.ReadAll(io.LimitReader(resp.Body, int64(liveMaxBytes)))
		if rerr != nil {
			return resp, nil, rerr
		}
		return resp, body, nil
	}

	resp, body, err := doFetch(preferAnthropicAuth)
	if err != nil {
		return nil, "", 0, err
	}
	if (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) && key != "" {
		resp2, body2, err2 := doFetch(!preferAnthropicAuth)
		if err2 == nil {
			resp, body = resp2, body2
		}
	}
	srvHdr := resp.Header.Get("Server")
	if resp.StatusCode != http.StatusOK {
		return nil, srvHdr, resp.StatusCode, fmt.Errorf("GET %s -> %s", endpoint, resp.Status)
	}
	rms, perr := decodeRawModels(body, true)
	if perr != nil {
		return nil, srvHdr, resp.StatusCode, fmt.Errorf("GET %s: %w", endpoint, perr)
	}
	return rms, srvHdr, resp.StatusCode, nil
}

type serverEnrichment struct {
	engine       string
	lmStudioByID map[string]lmStudioModel
	llamaProps   *llamaProps
}

type lmStudioModel struct {
	ID                  string   `json:"id"`
	DisplayName         string   `json:"display_name,omitempty"`
	Type                string   `json:"type"`
	Publisher           string   `json:"publisher"`
	Arch                string   `json:"arch"`
	CompatibilityType   string   `json:"compatibility_type"`
	Quantization        string   `json:"quantization"`
	ParamsString        string   `json:"params_string,omitempty"`
	State               string   `json:"state"`
	MaxContextLength    int64    `json:"max_context_length"`
	LoadedContextLength int64    `json:"loaded_context_length"`
	Vision              bool     `json:"vision,omitempty"`
	Capabilities        []string `json:"capabilities"`
	ReasoningOptions    []string `json:"reasoning_options,omitempty"`
	ReasoningDefault    string   `json:"reasoning_default,omitempty"`
}

type lmStudioV1Model struct {
	Type         string `json:"type"`
	Publisher    string `json:"publisher"`
	Key          string `json:"key"`
	DisplayName  string `json:"display_name"`
	Architecture string `json:"architecture"`
	Quantization *struct {
		Name string `json:"name"`
	} `json:"quantization"`
	ParamsString    string `json:"params_string"`
	LoadedInstances []struct {
		ID     string `json:"id"`
		Config *struct {
			ContextLength int64 `json:"context_length"`
		} `json:"config"`
	} `json:"loaded_instances"`
	MaxContextLength int64  `json:"max_context_length"`
	Format           string `json:"format"`
	Capabilities     *struct {
		Vision            bool `json:"vision"`
		TrainedForToolUse bool `json:"trained_for_tool_use"`
		Reasoning         *struct {
			AllowedOptions []string `json:"allowed_options"`
			Default        string   `json:"default"`
		} `json:"reasoning"`
	} `json:"capabilities"`
}

type llamaProps struct {
	ModelAlias                string `json:"model_alias"`
	TotalSlots                int    `json:"total_slots"`
	DefaultGenerationSettings *struct {
		NCtx int64 `json:"n_ctx"`
	} `json:"default_generation_settings"`
	Modalities *struct {
		Vision bool `json:"vision"`
		Audio  bool `json:"audio"`
	} `json:"modalities"`
}

func fetchServerEnrichment(ctx context.Context, origin, key string) serverEnrichment {
	if origin == "" {
		return serverEnrichment{}
	}
	ectx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	var mu sync.Mutex
	out := serverEnrichment{}

	get := func(path string) []byte {
		req, err := http.NewRequestWithContext(ectx, http.MethodGet, origin+path, nil)
		if err != nil {
			return nil
		}
		req.Header.Set("Accept", "application/json")
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		resp, err := probeHTTPClient.Do(req)
		if err != nil {
			return nil
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		return b
	}

	wg.Add(3)
	go func() {
		defer wg.Done()
		raw := get("/api/v1/models")
		if len(raw) == 0 {
			return
		}
		var env struct {
			Models []lmStudioV1Model `json:"models"`
		}
		if json.Unmarshal(raw, &env) != nil || len(env.Models) == 0 {
			return
		}
		isLMS := false
		byID := map[string]lmStudioModel{}
		for _, m := range env.Models {
			if m.Key == "" {
				continue
			}
			if m.Format != "" || m.MaxContextLength > 0 || m.LoadedInstances != nil || m.Capabilities != nil {
				isLMS = true
			}
			base := lmStudioModel{
				ID:                m.Key,
				DisplayName:       m.DisplayName,
				Type:              m.Type,
				Publisher:         m.Publisher,
				Arch:              m.Architecture,
				CompatibilityType: m.Format,
				ParamsString:      m.ParamsString,
				State:             "not-loaded",
				MaxContextLength:  m.MaxContextLength,
			}
			if m.Quantization != nil {
				base.Quantization = m.Quantization.Name
			}
			if m.Capabilities != nil {
				base.Vision = m.Capabilities.Vision
				if m.Capabilities.Vision {
					base.Capabilities = append(base.Capabilities, "vision")
				}
				if m.Capabilities.TrainedForToolUse {
					base.Capabilities = append(base.Capabilities, "tool_use")
				}
				if m.Capabilities.Reasoning != nil {
					base.ReasoningOptions = slices.Clone(m.Capabilities.Reasoning.AllowedOptions)
					base.ReasoningDefault = m.Capabilities.Reasoning.Default
				}
			}
			if len(m.LoadedInstances) > 0 {
				base.State = "loaded"
				if m.LoadedInstances[0].Config != nil && m.LoadedInstances[0].Config.ContextLength > 0 {
					base.LoadedContextLength = m.LoadedInstances[0].Config.ContextLength
				}
			}
			byID[m.Key] = base
			for _, inst := range m.LoadedInstances {
				if inst.ID == "" {
					continue
				}
				ic := base
				ic.ID = inst.ID
				ic.State = "loaded"
				if inst.Config != nil && inst.Config.ContextLength > 0 {
					ic.LoadedContextLength = inst.Config.ContextLength
				}
				byID[inst.ID] = ic
			}
		}
		if isLMS {
			mu.Lock()
			out.engine = "lmstudio"
			if out.lmStudioByID == nil {
				out.lmStudioByID = byID
			} else {
				// v1 carries richer metadata (display_name, reasoning options, vision bool); prefer it.
				for id, v := range byID {
					out.lmStudioByID[id] = v
				}
			}
			mu.Unlock()
		}
	}()
	go func() {
		defer wg.Done()
		raw := get("/api/v0/models")
		if len(raw) == 0 {
			return
		}
		var env struct {
			Data []lmStudioModel `json:"data"`
		}
		if json.Unmarshal(raw, &env) != nil || len(env.Data) == 0 {
			return
		}
		// Confirm LM Studio-specific fields so unrelated servers aren't misread.
		isLMS := false
		byID := map[string]lmStudioModel{}
		for _, m := range env.Data {
			if m.ID == "" {
				continue
			}
			if m.CompatibilityType != "" || m.LoadedContextLength > 0 || m.MaxContextLength > 0 || m.State != "" || m.Quantization != "" {
				isLMS = true
			}
			byID[m.ID] = m
		}
		if isLMS {
			mu.Lock()
			out.engine = "lmstudio"
			if out.lmStudioByID == nil {
				out.lmStudioByID = byID
			} else {
				// Fill any models missing from v1 without overwriting richer v1 entries.
				for id, v := range byID {
					if _, exists := out.lmStudioByID[id]; !exists {
						out.lmStudioByID[id] = v
					}
				}
			}
			mu.Unlock()
		}
	}()
	go func() {
		defer wg.Done()
		raw := get("/props")
		if len(raw) == 0 {
			return
		}
		var p llamaProps
		if json.Unmarshal(raw, &p) != nil {
			return
		}
		if p.DefaultGenerationSettings != nil || p.TotalSlots > 0 {
			mu.Lock()
			if out.engine == "" {
				out.engine = "llamacpp"
			}
			out.llamaProps = &p
			mu.Unlock()
		}
	}()
	wg.Wait()
	return out
}

func detectEngine(rms []rawLiveModel, enrich serverEnrichment, serverHeader string) string {
	for _, rm := range rms {
		if rm.Loom != nil {
			return "loom"
		}
	}
	if enrich.engine != "" {
		return enrich.engine
	}
	for _, rm := range rms {
		switch strings.ToLower(rm.OwnedBy) {
		case "vllm":
			return "vllm"
		case "sglang":
			return "sglang"
		case "llamacpp", "llama.cpp":
			return "llamacpp"
		case "library", "ollama":
			return "ollama"
		}
		if rm.Meta != nil && rm.Meta.NCtxTrain > 0 {
			return "llamacpp"
		}
		if rm.MaxModelLen > 0 {
			if strings.Contains(strings.ToLower(serverHeader), "sglang") {
				return "sglang"
			}
			return "vllm"
		}
	}
	hdr := strings.ToLower(serverHeader)
	switch {
	case strings.Contains(hdr, "ollama"):
		return "ollama"
	case strings.Contains(hdr, "llama"):
		return "llamacpp"
	case strings.Contains(hdr, "vllm"):
		return "vllm"
	case strings.Contains(hdr, "sglang"):
		return "sglang"
	}
	return ""
}

// probeAPIEndpoints tests whether /chat/completions, /responses, and /messages
// are implemented on baseURL by sending a tiny invalid-body POST carrying only
// the model id. Supported endpoints return a 400/422 validation error without
// running inference; unsupported ones return 404/405/501 or an endpoint-error
// payload.
func probeAPIEndpoints(ctx context.Context, baseURL, modelID, key string) []string {
	pctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()

	payloadModel := modelID
	noModel := payloadModel == ""
	if noModel {
		payloadModel = "__eagent_probe__"
	}
	payload, _ := json.Marshal(map[string]any{"model": payloadModel})

	type probeResp struct {
		status int
		body   string
		err    error
	}
	doPost := func(path string, extra map[string]string) probeResp {
		req, err := http.NewRequestWithContext(pctx, http.MethodPost, strings.TrimRight(baseURL, "/")+path, bytes.NewReader(payload))
		if err != nil {
			return probeResp{err: err}
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		for k, v := range extra {
			req.Header.Set(k, v)
		}
		resp, err := probeHTTPClient.Do(req)
		if err != nil {
			return probeResp{err: err}
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return probeResp{status: resp.StatusCode, body: string(b)}
	}

	// When no model id was discovered, check a bogus path so catch-all 400
	// routers (e.g. model-first routers) don't falsely match every protocol.
	var bogusBody string
	var bogusStatus int
	if noModel {
		b := doPost("/_eagent_probe_unknown", nil)
		bogusStatus, bogusBody = b.status, strings.TrimSpace(b.body)
	}

	type target struct {
		proto string
		path  string
		hdrs  map[string]string
	}
	bearer := map[string]string{}
	anth := map[string]string{"anthropic-version": "2023-06-01"}
	if key != "" {
		bearer["Authorization"] = "Bearer " + key
		anth["x-api-key"] = key
	}
	targets := []target{
		{llm.ProtocolChat, "/chat/completions", bearer},
		{llm.ProtocolResponses, "/responses", bearer},
		{llm.ProtocolAnthropic, "/messages", anth},
	}

	type result struct {
		proto string
		ok    bool
	}
	ch := make(chan result, len(targets))
	for _, t := range targets {
		go func(t target) {
			r := doPost(t.path, t.hdrs)
			if r.err != nil {
				ch <- result{t.proto, false}
				return
			}
			// If /messages rejected x-api-key with 401/403 and a key is set,
			// retry with Bearer (some OpenAI-front proxies use Bearer for all).
			if t.proto == llm.ProtocolAnthropic && (r.status == http.StatusUnauthorized || r.status == http.StatusForbidden) && key != "" {
				r = doPost(t.path, map[string]string{"Authorization": "Bearer " + key, "anthropic-version": "2023-06-01"})
				if r.err != nil {
					ch <- result{t.proto, false}
					return
				}
			}
			if noModel && bogusStatus == r.status && bogusBody != "" && strings.TrimSpace(r.body) == bogusBody {
				ch <- result{t.proto, false}
				return
			}
			ch <- result{t.proto, classifyEndpointProbe(r.status, r.body)}
		}(t)
	}
	var supported []string
	for range targets {
		res := <-ch
		if res.ok {
			supported = append(supported, res.proto)
		}
	}
	return orderProtocols(supported)
}

func classifyEndpointProbe(status int, body string) bool {
	if status == 0 {
		return false
	}
	lower := strings.ToLower(body)
	switch status {
	case http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusNotImplemented, http.StatusGone:
		return false
	case http.StatusOK:
		// LM Studio returns HTTP 200 {"error":"Unexpected endpoint or method..."}
		// for unknown routes; reject any 200 that carries an error field.
		var obj map[string]json.RawMessage
		if json.Unmarshal([]byte(body), &obj) == nil {
			if _, hasErr := obj["error"]; hasErr {
				return false
			}
		}
		return !looksLikeMissingEndpoint(lower)
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return !looksLikeMissingEndpoint(lower)
	default:
		return false
	}
}

func looksLikeMissingEndpoint(lower string) bool {
	for _, phrase := range []string{
		"unexpected endpoint",
		"unknown endpoint",
		"unknown service endpoint",
		"unknown path",
		"unknown route",
		"route not found",
		"endpoint not found",
		"path not found",
		"not implemented",
		"unimplemented",
		"method not allowed",
		"invalid url",
		"404 page not found",
		"cannot post ",
	} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

func orderProtocols(in []string) []string {
	canon := []string{llm.ProtocolChat, llm.ProtocolResponses, llm.ProtocolAnthropic}
	var out []string
	for _, p := range canon {
		if slices.Contains(in, p) {
			out = append(out, p)
		}
	}
	return out
}

func originOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

func engineDisplayName(engine string) string {
	switch engine {
	case "lmstudio":
		return "LM Studio"
	case "loom":
		return "Loom"
	case "llamacpp":
		return "llama.cpp"
	case "vllm":
		return "vLLM"
	case "sglang":
		return "SGLang"
	case "ollama":
		return "Ollama"
	default:
		return ""
	}
}

func suggestServerID(engine, baseURL string) string {
	u, _ := url.Parse(baseURL)
	host := ""
	port := ""
	if u != nil {
		host = u.Hostname()
		port = u.Port()
	}
	prefix := engine
	if prefix == "" {
		if isLoopback(host) {
			prefix = "local"
		} else {
			prefix = slugify(host)
		}
	} else if !isLoopback(host) && host != "" {
		shortHost := strings.Split(host, ".")[0]
		if slug := slugify(shortHost); slug != "" && slug != prefix {
			prefix = prefix + "-" + slug
		}
	}
	if port != "" {
		prefix = prefix + "-" + port
	}
	id := slugify(prefix)
	if id == "" || !ValidServerID(id) {
		id = "local-server"
	}
	if len(id) > 48 {
		id = strings.TrimRight(id[:48], "-_")
	}
	return id
}

func suggestServerLabel(engine, baseURL string) string {
	u, _ := url.Parse(baseURL)
	hostPort := strings.TrimPrefix(strings.TrimPrefix(baseURL, "https://"), "http://")
	if u != nil && u.Host != "" {
		hostPort = u.Host
	}
	if name := engineDisplayName(engine); name != "" {
		return fmt.Sprintf("%s (%s)", name, hostPort)
	}
	if u != nil && isLoopback(u.Hostname()) {
		return fmt.Sprintf("Local (%s)", hostPort)
	}
	return hostPort
}

func slugify(s string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastDash = false
		case r == '-' || r == '_' || r == '.' || r == ':' || r == ' ':
			if b.Len() > 0 && !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-_")
}

func uniqueServerID(base string, existing []RegisteredServer) string {
	taken := func(id string) bool {
		if !ValidServerID(id) {
			return true
		}
		for _, s := range existing {
			if s.ID == id {
				return true
			}
		}
		return false
	}
	if !taken(base) {
		return base
	}
	for n := 2; n < 1000; n++ {
		suffix := fmt.Sprintf("-%d", n)
		head := base
		if len(head)+len(suffix) > 48 {
			head = strings.TrimRight(head[:48-len(suffix)], "-_")
		}
		cand := head + suffix
		if !taken(cand) {
			return cand
		}
	}
	return base
}

func buildCustomModels(pr Provider, rms []rawLiveModel, enrich serverEnrichment) []Model {
	seen := map[string]bool{}
	var out []Model
	for _, rm := range rms {
		if rm.ID == "" || seen[rm.ID] {
			continue
		}
		// Apply LM Studio /api/v1/models or /api/v0/models enrichment before
		// filtering so embedding models are skipped cleanly and VLM/context/reasoning
		// metadata is preserved.
		if lms, ok := enrich.lmStudioByID[rm.ID]; ok {
			if rm.DisplayName == "" && lms.DisplayName != "" {
				rm.DisplayName = lms.DisplayName
			}
			if rm.Type == "" {
				rm.Type = lms.Type
			}
			if rm.State == "" {
				rm.State = lms.State
			}
			if rm.Arch == "" {
				rm.Arch = lms.Arch
			}
			if rm.Quantization == "" {
				rm.Quantization = lms.Quantization
			}
			if rm.LoadedContextLength == 0 {
				rm.LoadedContextLength = lms.LoadedContextLength
			}
			if rm.MaxContextLength == 0 {
				rm.MaxContextLength = lms.MaxContextLength
			}
			if len(rm.Capabilities) == 0 && len(lms.Capabilities) > 0 {
				rm.Capabilities, _ = json.Marshal(lms.Capabilities)
			}
			if len(rm.SupportedReasoningLevels) == 0 && len(lms.ReasoningOptions) > 0 {
				rm.SupportedReasoningLevels = mapLMStudioReasoningOptions(lms.ReasoningOptions)
			}
			if rm.DefaultReasoningLevel == "" && lms.ReasoningDefault != "" {
				rm.DefaultReasoningLevel = mapLMStudioReasoningDefault(lms.ReasoningDefault)
			}
		}
		if isNonGenerativeModel(rm) {
			continue
		}
		seen[rm.ID] = true
		m := liveModelFor(pr, rm)
		if m.Context == 0 && enrich.llamaProps != nil && enrich.llamaProps.DefaultGenerationSettings != nil && enrich.llamaProps.DefaultGenerationSettings.NCtx > 0 {
			m.Context = int(enrich.llamaProps.DefaultGenerationSettings.NCtx)
		}
		if !m.Vision && enrich.llamaProps != nil && enrich.llamaProps.Modalities != nil && enrich.llamaProps.Modalities.Vision {
			m.Vision = true
		}
		if m.Protocol == "" {
			m.Protocol = pr.Protocol
		}
		if len(m.Protocols) == 0 {
			if p := declaredModelProtocol(rm); p != "" {
				m.Protocols = []string{p}
			} else if len(pr.Protocols) > 0 {
				m.Protocols = slices.Clone(pr.Protocols)
			} else if m.Protocol != "" {
				m.Protocols = []string{m.Protocol}
			}
		}
		out = append(out, m)
	}
	return out
}

func mapLMStudioReasoningOptions(opts []string) []string {
	var out []string
	add := func(e string) {
		if EffortIndex(e) < len(EffortOrder) && !slices.Contains(out, e) {
			out = append(out, e)
		}
	}
	for _, raw := range opts {
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "off", "none":
			add("none")
		case "on":
			// Toggle-only models (e.g. Gemma 4, Qwen 3) expose on/off rather than
			// granular effort levels; mapping "on" to "medium" lets NearestEffort
			// treat low/medium/high as on and none as off.
			add("medium")
		case "minimal", "low", "medium", "high", "xhigh", "max":
			add(strings.ToLower(strings.TrimSpace(raw)))
		}
	}
	return out
}

func mapLMStudioReasoningDefault(def string) string {
	switch strings.ToLower(strings.TrimSpace(def)) {
	case "off":
		return "none"
	case "on":
		return "on"
	default:
		return strings.ToLower(strings.TrimSpace(def))
	}
}

func isNonGenerativeModel(rm rawLiveModel) bool {
	if strings.EqualFold(rm.Type, "embeddings") || strings.EqualFold(rm.Type, "embedding") {
		return true
	}
	if rm.Loom != nil && rm.Loom.Kind != "" {
		switch strings.ToLower(rm.Loom.Kind) {
		case "text", "chat", "multimodal", "vlm", "llm":
			return false
		default:
			return true
		}
	}
	return false
}

func declaredModelProtocol(rm rawLiveModel) string {
	if rm.Loom == nil || rm.Loom.Endpoint == "" {
		return ""
	}
	ep := strings.TrimRight(strings.TrimSpace(rm.Loom.Endpoint), "/")
	switch {
	case strings.HasSuffix(ep, "/chat/completions"):
		return llm.ProtocolChat
	case strings.HasSuffix(ep, "/responses"):
		return llm.ProtocolResponses
	case strings.HasSuffix(ep, "/messages"):
		return llm.ProtocolAnthropic
	default:
		return ""
	}
}

func probeCustomProviderHTTP(ctx context.Context, pr Provider) ([]Model, error) {
	key := ""
	if pr.KeyEnv != "" {
		key = os.Getenv(pr.KeyEnv)
		if key == "" {
			return nil, fmt.Errorf("no %s in the environment", pr.KeyEnv)
		}
	}
	rms, _, _, err := fetchRawModels(ctx, pr.BaseURL, key, pr.Protocol == llm.ProtocolAnthropic)
	if err != nil {
		return nil, err
	}
	enrich := fetchServerEnrichment(ctx, originOf(pr.BaseURL), key)
	return buildCustomModels(pr, rms, enrich), nil
}
