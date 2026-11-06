package config

import (
	"fmt"
	"slices"
	"strings"
)

// PickRoute applies a provider + model + reasoning-effort pick to an actor:
// the one place a pick from the new-session dialog or a running session's
// settings becomes a route. The model must be in the static catalog or the
// cached live list; the network is never touched. The actor keeps its own
// limits; picking a different model drops its fallback chain, which belonged
// to the old model. An empty effort keeps the actor's when the model accepts
// it, else takes the nearest level the model offers. An empty provider is
// inferred from the model.
func PickRoute(project string, a Actor, providerID, modelID, effort string) (Actor, error) {
	if providerID == "" {
		// No provider named: the actor's own, when it lists the model, or
		// the one provider that does.
		var err error
		if providerID, err = providerFor(project, a, modelID); err != nil {
			return a, err
		}
	}
	m, pr, ok := ResolveLiveModel(project, providerID, modelID)
	if !ok {
		return a, fmt.Errorf("model %q is not listed by provider %q (live models need one catalog fetch first)", modelID, providerID)
	}
	if effort != "" {
		if EffortIndex(effort) == len(EffortOrder) {
			return a, fmt.Errorf("effort %q is not a known level (%s)", effort, strings.Join(EffortOrder, ", "))
		}
		if len(m.Efforts) > 0 && !slices.Contains(m.Efforts, effort) {
			return a, fmt.Errorf("model %q does not list effort %q (known: %s)", modelID, effort, strings.Join(m.Efforts, ", "))
		}
	}
	if a.BaseURL != pr.BaseURL || a.Model != m.ID {
		a.Protocol, a.BaseURL, a.Model, a.APIKeyEnv, a.Fallback = m.ProtocolFor(), pr.BaseURL, m.ID, pr.KeyEnv, nil
	}
	switch {
	case effort != "":
		a.ReasoningEffort = effort
	case len(m.Efforts) > 0 && !slices.Contains(m.Efforts, a.ReasoningEffort):
		a.ReasoningEffort = NearestEffort(m.Efforts, a.ReasoningEffort)
	}
	return a, nil
}

// NearestEffort is the offered level closest to want (the lower one on a
// tie); with no want, medium when offered, else the middle level.
func NearestEffort(offered []string, want string) string {
	if len(offered) == 0 {
		return want
	}
	if want == "" || EffortIndex(want) == len(EffortOrder) {
		if slices.Contains(offered, "medium") {
			return "medium"
		}
		return offered[len(offered)/2]
	}
	best, dist := offered[0], len(EffortOrder)
	for _, e := range offered {
		d := EffortIndex(e) - EffortIndex(want)
		if d < 0 {
			d = -d
		}
		if d < dist || d == dist && EffortIndex(e) < EffortIndex(best) {
			best, dist = e, d
		}
	}
	return best
}

// providerFor finds the provider for a model named without one.
func providerFor(project string, a Actor, modelID string) (string, error) {
	if pr, ok := ProviderForURL(a.BaseURL); ok {
		if _, _, ok := ResolveLiveModel(project, pr.ID, modelID); ok {
			return pr.ID, nil
		}
	}
	var found []string
	for _, m := range KnownModels(project) {
		if m.ID == modelID && !slices.Contains(found, m.Provider) {
			found = append(found, m.Provider)
		}
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return "", fmt.Errorf("no provider lists model %q (live models need one catalog fetch first)", modelID)
	default:
		return "", fmt.Errorf("model %q is offered by %s; pick a provider", modelID, strings.Join(found, ", "))
	}
}

// KnownModels are the models a pick can name: the built-in catalog plus the
// providers' live lists as last cached. It never touches the network.
func KnownModels(project string) []Model {
	var live []Model
	if c := readLiveCache(project); c != nil {
		live = c.Models
	}
	return MergeLive(Models, live)
}
