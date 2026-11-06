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
// it, else takes the nearest level the model offers.
func PickRoute(project string, a Actor, providerID, modelID, effort string) (Actor, error) {
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
