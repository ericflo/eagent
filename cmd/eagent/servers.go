package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/llm"
	"github.com/ericflo/eagent/internal/settings"
)

type serverCLIOpts struct {
	id       string
	label    string
	protocol string
	keyEnv   string
	note     string
	effort   string
	noProbe  bool
	asJSON   bool
}

func cmdServers(project string, args []string, opts serverCLIOpts) int {
	sub := ""
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	switch sub {
	case "", "list":
		servers, err := config.LoadServers(project)
		if err != nil {
			return fail(err)
		}
		models := config.KnownModels(project)
		byServer := map[string][]config.Model{}
		for _, m := range models {
			byServer[m.Provider] = append(byServer[m.Provider], m)
		}
		if opts.asJSON {
			type row struct {
				Server config.RegisteredServer `json:"server"`
				Models []config.Model          `json:"models"`
			}
			out := make([]row, 0, len(servers))
			for _, s := range servers {
				ms := byServer[s.ID]
				if ms == nil {
					ms = []config.Model{}
				}
				out = append(out, row{Server: s, Models: ms})
			}
			raw, _ := json.MarshalIndent(out, "", "  ")
			fmt.Println(string(raw))
			return 0
		}
		if len(servers) == 0 {
			fmt.Printf("no custom/local servers registered in %s\n", config.ServersPath(project))
			fmt.Println("add one with: eagent servers add http://127.0.0.1:1234")
			return 0
		}
		for _, s := range servers {
			engine := ""
			if s.Engine != "" {
				engine = " [" + s.Engine + "]"
			}
			auth := "no key"
			if s.KeyEnv != "" {
				if os.Getenv(s.KeyEnv) != "" {
					auth = "$" + s.KeyEnv + " (set)"
				} else {
					auth = "$" + s.KeyEnv + " (not set)"
				}
			}
			protos := s.Protocol
			if len(s.Protocols) > 0 {
				protos = strings.Join(s.Protocols, ", ") + " (default: " + s.Protocol + ")"
			}
			fmt.Printf("  %-20s %s%s\n", s.ID, s.Label, engine)
			fmt.Printf("  %-20s url: %s · api: %s · auth: %s\n", "", s.BaseURL, protos, auth)
			ms := byServer[s.ID]
			if len(ms) == 0 {
				fmt.Printf("  %-20s models: (none cached; run `eagent servers refresh %s`)\n", "", s.ID)
			} else {
				for _, m := range ms {
					fmt.Printf("  %-20s   - %s%s\n", "", m.ID, modelMetaLine(m))
				}
			}
		}
		return 0

	case "probe":
		if len(args) < 1 {
			return fail(errors.New("usage: eagent servers probe URL [--protocol P] [--key-env VAR] [--id ID] [--label LABEL]"))
		}
		res, err := config.ProbeServer(ctx, config.ProbeOptions{
			URL:      args[0],
			Protocol: opts.protocol,
			KeyEnv:   opts.keyEnv,
			ID:       opts.id,
			Label:    opts.label,
			Note:     opts.note,
		})
		if err != nil {
			return fail(err)
		}
		printProbeResult(res, opts.asJSON, false)
		return 0

	case "add":
		if len(args) < 1 {
			return fail(errors.New("usage: eagent servers add URL [--id ID] [--label LABEL] [--protocol P] [--key-env VAR] [--no-probe]"))
		}
		res, err := config.RegisterServer(ctx, project, config.ProbeOptions{
			URL:      args[0],
			Protocol: opts.protocol,
			KeyEnv:   opts.keyEnv,
			ID:       opts.id,
			Label:    opts.label,
			Note:     opts.note,
		}, opts.noProbe)
		if err != nil {
			return fail(err)
		}
		printProbeResult(res, opts.asJSON, true)
		return 0

	case "refresh":
		if len(args) >= 1 {
			res, err := config.RefreshServer(ctx, project, args[0])
			if err != nil {
				return fail(err)
			}
			printProbeResult(res, opts.asJSON, false)
			return 0
		}
		results, errs, err := config.RefreshAllServers(ctx, project)
		if err != nil {
			return fail(err)
		}
		if opts.asJSON {
			raw, _ := json.MarshalIndent(map[string]any{"servers": results, "errors": errs}, "", "  ")
			fmt.Println(string(raw))
			if len(errs) > 0 {
				return 1
			}
			return 0
		}
		if len(results) == 0 && len(errs) == 0 {
			fmt.Println("no registered servers to refresh")
			return 0
		}
		for id, res := range results {
			fmt.Printf("  ✓ %-20s %s · %d model%s (%s)\n", id, res.Server.BaseURL, len(res.Models), pluralS(len(res.Models)), strings.Join(res.Server.Protocols, ", "))
		}
		for id, msg := range errs {
			fmt.Printf("  ✗ %-20s %s\n", id, msg)
		}
		if len(errs) > 0 {
			return 1
		}
		return 0

	case "remove", "rm", "delete":
		if len(args) < 1 {
			return fail(errors.New("usage: eagent servers remove ID"))
		}
		if err := config.DeleteServer(ctx, project, args[0]); err != nil {
			return fail(err)
		}
		if opts.asJSON {
			fmt.Printf("{\"removed\":%q}\n", args[0])
		} else {
			fmt.Printf("removed server %s\n", args[0])
		}
		return 0

	case "use":
		if len(args) < 1 {
			return fail(errors.New("usage: eagent servers use ID [MODEL] [--protocol P] [--effort E]"))
		}
		return cmdServersUse(ctx, project, args, opts)

	default:
		return fail(fmt.Errorf("unknown servers command %q (list, probe, add, refresh, remove, use)", sub))
	}
}

func cmdServersUse(ctx context.Context, project string, args []string, opts serverCLIOpts) int {
	serverID := args[0]
	pr, ok := config.ProviderByIDFor(project, serverID)
	if !ok {
		return fail(fmt.Errorf("no provider or registered server named %q (run `eagent servers` to list them)", serverID))
	}
	modelID := ""
	if len(args) >= 2 {
		modelID = args[1]
	} else {
		for _, m := range config.KnownModels(project) {
			if m.Provider == pr.ID {
				modelID = m.ID
				break
			}
		}
		if modelID == "" && pr.Custom {
			if res, err := config.RefreshServer(ctx, project, pr.ID); err == nil && len(res.Models) > 0 {
				modelID = res.Models[0].ID
			}
		}
	}
	if modelID == "" {
		return fail(fmt.Errorf("server %q has no cached models; pass a model ID explicitly: eagent servers use %s MODEL", pr.ID, pr.ID))
	}
	if opts.protocol != "" && opts.protocol != llm.ProtocolChat && opts.protocol != llm.ProtocolResponses && opts.protocol != llm.ProtocolAnthropic {
		return fail(fmt.Errorf("unknown protocol %q (use openai-chat, openai-responses, or anthropic)", opts.protocol))
	}
	if opts.effort != "" && config.EffortIndex(opts.effort) == len(config.EffortOrder) {
		return fail(fmt.Errorf("unknown effort %q (use %s)", opts.effort, strings.Join(config.EffortOrder, ", ")))
	}
	res := config.Resolve(project, "", "")
	if res.LoadError != "" {
		return fail(errors.New(res.LoadError))
	}
	// The save skips what the environment pins, so a pinned field of the
	// route would leave the file with half of it: another server's address
	// with the pinned model. Nothing is written instead.
	pinned := config.EnvOverrides()
	for _, field := range []string{"model", "base_url", "protocol", "api_key_env"} {
		if name, ok := pinned["/model/"+field]; ok {
			return fail(fmt.Errorf("%s sets eagent's %s here, so the model cannot be changed in the file; unset it first", name, field))
		}
	}
	// The save writes the route as its difference from the preset the file
	// names. Where loading lays another preset in its place (a variable,
	// the file's default_config or a saved preset names it), that
	// difference would be laid over the wrong preset and run as neither.
	// Nothing is written instead. Loading says which it laid, and why.
	own, _ := config.ResolvePreset(res.File.Preset)
	if laid, from, err := config.LaidPreset(project, nil); err == nil && from != "" && presetsDiffer(laid, own) {
		cur, _ := config.Load(project, "") // (names the saved preset laid, and what lays it)
		why, undo := laidAbove(cur, laid, from)
		return fail(fmt.Errorf("%s, in place of the file's own, so a model written to the file would not run as written; nothing was changed. %s", why, undo))
	}
	cfg := res.Effective
	a := &cfg.Model
	picked, err := config.PickRoute(project, *a, pr.ID, modelID, opts.effort)
	if err != nil {
		// If the user typed a model ID not yet in the live cache on a custom
		// server, apply it directly with the server's route details.
		if pr.Custom {
			picked = *a
			picked.BaseURL = pr.BaseURL
			picked.Model = modelID
			picked.APIKeyEnv = pr.KeyEnv
			picked.Protocol = pr.Protocol
			picked.Fallback = nil
			// No list gives this model's window: unknown, not the old model's.
			picked.ContextTokens = 0
			if opts.effort != "" {
				picked.ReasoningEffort = opts.effort
			}
		} else {
			return fail(err)
		}
	}
	if opts.protocol != "" {
		picked.Protocol = opts.protocol
		// Chat Completions and Responses both read replay_reasoning;
		// Anthropic Messages has no such setting.
		if picked.Protocol == llm.ProtocolAnthropic {
			picked.ReplayReasoning = nil
		}
	}
	*a = picked
	// If the model has a known context window smaller than RolloverTokens, pull
	// RolloverTokens down to 75% of the window (at least 20k) so validation stays clean.
	if w := cfg.Model.ContextTokens; w > 0 && cfg.RolloverTokens > w*3/4 {
		cfg.RolloverTokens = max(20_000, w*3/4)
	}
	rawCfg, err := json.Marshal(cfg)
	if err != nil {
		return fail(err)
	}
	svc := &settings.Service{Project: project}
	etag := res.File.ETag
	saveRes, err := svc.Save(ctx, settings.Request{
		Config:     rawCfg,
		BasePreset: res.File.Preset,
		IfMatch:    &etag,
	})
	if err != nil {
		return fail(err)
	}
	if opts.asJSON {
		raw, _ := json.MarshalIndent(saveRes, "", "  ")
		fmt.Println(string(raw))
		return 0
	}
	fmt.Printf("updated %s\n", saveRes.Saved.Path)
	// The line that says what eagent runs is what loading says runs here,
	// whatever else is laid over the file.
	now, err := config.Load(project, "")
	switch {
	case err != nil:
		fmt.Printf("  written: %s @ %s (%s). The configuration does not load here, so no session starts until that is put right: %v\n", cfg.Model.Model, cfg.Model.BaseURL, cfg.Model.Protocol, err)
	case !otherRoute(now.Model, cfg.Model):
		fmt.Printf("  eagent -> %s @ %s (%s)\n", cfg.Model.Model, cfg.Model.BaseURL, cfg.Model.Protocol)
	case now.Name != "":
		// A saved preset laid over the file sets part of the route: what
		// was written runs where none is laid over it. Both routes in
		// full, and the parts in which they differ.
		how := now.NameFrom + " lays it here: unset it"
		if now.NameFrom == "default_config" {
			how = "the project's default_config lays it here: `eagent presets use default` clears that"
		}
		fmt.Printf("  the saved preset %q is laid over the project file here, so eagent runs %s. That differs from what was written (%s) in its %s. What was written runs where no saved preset is laid over the file; %s.\n", now.Name, routeText(now.Model), routeText(cfg.Model), routeParts(now.Model, cfg.Model), how)
	default:
		fmt.Printf("  eagent runs %s here. That differs from what was written (%s) in its %s: something outside the project file sets it here.\n", routeText(now.Model), routeText(cfg.Model), routeParts(now.Model, cfg.Model))
	}
	return 0
}

// routeText is a route in full: its model, address, protocol and the
// variable its key is read from.
func routeText(m config.ModelRoute) string {
	key := "no key variable"
	if m.APIKeyEnv != "" {
		key = "key $" + m.APIKeyEnv
	}
	return fmt.Sprintf("%s @ %s (%s, %s)", m.Model, m.BaseURL, m.Protocol, key)
}

// routeParts names the parts in which two routes differ, "" for one route.
func routeParts(a, b config.ModelRoute) string {
	var parts []string
	for _, p := range []struct {
		name  string
		other bool
	}{
		{"model", a.Model != b.Model},
		{"address", strings.TrimRight(a.BaseURL, "/") != strings.TrimRight(b.BaseURL, "/")},
		{"protocol", a.Protocol != b.Protocol},
		{"key variable", a.APIKeyEnv != b.APIKeyEnv},
	} {
		if p.other {
			parts = append(parts, p.name)
		}
	}
	return strings.Join(parts, ", ")
}

func printProbeResult(res config.ProbeResult, asJSON, saved bool) {
	if asJSON {
		raw, _ := json.MarshalIndent(res, "", "  ")
		fmt.Println(string(raw))
		return
	}
	s := res.Server
	verb := "probed"
	if saved {
		verb = "registered"
	}
	fmt.Printf("%s %s (%s)\n", verb, s.ID, s.Label)
	fmt.Printf("  base_url:  %s\n", s.BaseURL)
	if s.Engine != "" {
		fmt.Printf("  engine:    %s\n", s.Engine)
	}
	protos := s.Protocol
	if len(s.Protocols) > 0 {
		parts := make([]string, 0, len(s.Protocols))
		for _, p := range s.Protocols {
			if p == s.Protocol {
				parts = append(parts, p+"*")
			} else {
				parts = append(parts, p)
			}
		}
		if !slices.Contains(s.Protocols, s.Protocol) {
			parts = append(parts, s.Protocol+"* (manual)")
		}
		protos = strings.Join(parts, ", ")
	}
	fmt.Printf("  protocols: %s\n", protos)
	if s.KeyEnv != "" {
		fmt.Printf("  api_key:   $%s\n", s.KeyEnv)
	} else {
		fmt.Printf("  api_key:   none\n")
	}
	fmt.Printf("  models (%d):\n", len(res.Models))
	for _, m := range res.Models {
		fmt.Printf("    - %s%s\n", m.ID, modelMetaLine(m))
	}
	for _, w := range res.Warnings {
		fmt.Printf("  warning: %s\n", w)
	}
	if saved {
		fmt.Printf("\nuse in this project: eagent servers use %s\n", s.ID)
	}
}

func modelMetaLine(m config.Model) string {
	var parts []string
	if m.Context > 0 {
		if m.Context >= 1_000_000 {
			parts = append(parts, fmt.Sprintf("%.1fM ctx", float64(m.Context)/1_000_000))
		} else if m.Context >= 1000 {
			parts = append(parts, fmt.Sprintf("%dk ctx", m.Context/1000))
		} else {
			parts = append(parts, fmt.Sprintf("%d ctx", m.Context))
		}
	}
	if m.Vision {
		parts = append(parts, "vision")
	}
	if len(m.Efforts) > 0 {
		parts = append(parts, "efforts: "+strings.Join(m.Efforts, "/"))
	}
	if m.Note != "" {
		parts = append(parts, m.Note)
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, " · ") + ")"
}

// otherRoute says whether b is another route than a: another model, or the
// same one reached at another address, by another protocol or with another
// key variable.
func otherRoute(a, b config.ModelRoute) bool { return routeParts(a, b) != "" }

// presetsDiffer says whether two built-in presets give different settings.
// The empty name is the defaults, which a preset can be too.
func presetsDiffer(a, b string) bool {
	lay := func(name string) []byte {
		c := config.Defaults()
		if preset, ok := config.Presets[name]; ok {
			preset(&c)
		}
		raw, _ := json.Marshal(c)
		return raw
	}
	return !bytes.Equal(lay(a), lay(b))
}

// laidAbove says what named the preset laid in place of the project
// file's own "preset" key, and how that is undone. laid and from are
// config.LaidPreset's; cfg, loaded beside them, names the saved preset
// when that is what names it.
func laidAbove(cfg config.Config, laid, from string) (why, undo string) {
	why = fmt.Sprintf("%s names the preset %s here", from, laid)
	if from == "default_config" {
		why = fmt.Sprintf("the file's default_config names the preset %s here", laid)
	}
	if from == "bundle" {
		from = cfg.NameFrom
		lays := from
		if from == "default_config" {
			lays = "the file's default_config"
		}
		why = fmt.Sprintf("the saved preset %q, which %s lays over the project file here, names the preset %s", cfg.Name, lays, laid)
	}
	undo = "Unset " + from + " first"
	if from == "default_config" {
		undo = "`eagent presets use default` clears the file's default_config"
	}
	return why, undo
}
