package relay

import (
	"errors"
	"strings"
	"sync"
	"time"
)

// Model and effort choices for the agents a relay runs. A profile without a
// model or effort passes none, so the agent starts with what its own
// configuration says (Codex's config.toml, Claude Code's settings.json);
// these read that configuration and the models each agent can run, so a
// picker can say what leaving the choice alone gives and offer the rest.
// Each agent answers for itself (its defaults, models and efforts methods);
// these functions look the agent up by name.

// ModelChoice is one model an agent can run.
type ModelChoice struct {
	ID, Label, Detail string
}

// catalogs is how an agent's own CLI is asked for its models
// (`pi --list-models`, `agy models`): the command's output, with env added
// to the environment. OfflineCatalogs replaces it.
var catalogs = struct {
	sync.Mutex
	run func(timeout time.Duration, env []string, name string, args ...string) ([]byte, error)
}{run: func(timeout time.Duration, env []string, name string, args ...string) ([]byte, error) {
	cmd, cancel := bounded(timeout, name, args...)
	defer cancel()
	if len(env) > 0 {
		cmd.Env = append(cmd.Environ(), env...)
	}
	return cmd.Output()
}}

func catalog(timeout time.Duration, env []string, name string, args ...string) ([]byte, error) {
	catalogs.Lock()
	run := catalogs.run
	catalogs.Unlock()
	return run(timeout, env, name, args...)
}

// OfflineCatalogs makes every agent's model list come back as it does where
// the agent is not installed, without starting its CLI. Tests outside this
// package call it from TestMain: a test must never run the agents on the
// developer's machine (pi takes a second or two to list its models, and
// agy asks the network for them).
func OfflineCatalogs() {
	catalogs.Lock()
	defer catalogs.Unlock()
	catalogs.run = func(time.Duration, []string, string, ...string) ([]byte, error) {
		return nil, errors.New("agent CLIs are not run in tests")
	}
}

// standardEfforts are the levels accepted by the Claude and Antigravity CLIs.
var standardEfforts = []string{"low", "medium", "high", "xhigh", "max"}

// maxModelChoices bounds an agent's model list, which a settings
// descriptor carries whole.
const maxModelChoices = 100

// choiceList collects model choices without duplicates, up to a bound.
type choiceList struct {
	out  []ModelChoice
	seen map[string]bool
	max  int // 0 means maxModelChoices
}

func (l *choiceList) add(c ModelChoice) {
	if l.seen == nil {
		l.seen = map[string]bool{}
	}
	limit := l.max
	if limit == 0 {
		limit = maxModelChoices
	}
	if c.ID != "" && !l.seen[c.ID] && len(l.out) < limit {
		l.seen[c.ID] = true
		l.out = append(l.out, c)
	}
}

// AgentDefaults is the model and reasoning effort an agent starts with when
// eagent passes neither, read from its own configuration (Codex's
// config.toml model and model_reasoning_effort, Claude Code's settings.json
// model and effortLevel, and so on). Either is "" when not set there.
func AgentDefaults(agent string) (model, effort string) {
	if a := AgentByName(agent); a != nil {
		return a.defaults()
	}
	return "", ""
}

// AgentModels lists the models an agent can be started with, best first.
func AgentModels(agent, project string) []ModelChoice {
	if a := AgentByName(agent); a != nil {
		return a.models(project)
	}
	return nil
}

// AgentEfforts lists the reasoning efforts an agent accepts, in order.
func AgentEfforts(agent string) []string {
	if a := AgentByName(agent); a != nil {
		return a.efforts()
	}
	return nil
}

// ModelListName names the suggestion list of an agent's models in a
// settings descriptor ("codex_models"). A descriptor holds at most eight
// lists, so an agent that runs another's models (a wrapper around pi)
// shares that agent's list by implementing modelsOf.
func ModelListName(agent string) string {
	if a, ok := AgentByName(agent).(modelSharer); ok {
		agent = a.modelsOf()
	}
	return agent + "_models"
}

// AgentName is how people call an agent ("Claude Code", "Codex").
func AgentName(agent string) string {
	if a := AgentByName(agent); a != nil {
		return a.DisplayName()
	}
	return agent
}

// ModelLabel is how a model reads for an agent: its picker label, else its id.
func ModelLabel(agent, project, model string) string {
	a := AgentByName(agent)
	if a == nil {
		return model
	}
	if _, ok := a.(plainModels); ok {
		return model
	}
	for _, c := range a.models(project) {
		if c.ID == model && c.Label != "" {
			return a.modelLabel(c)
		}
	}
	return model
}

// plainModels marks an agent whose models read as their ids: a label never
// needs its list. pi's list comes from a subprocess of up to eight
// seconds, and labels are made on eagent's loop at every start, restart and
// reattach.
type plainModels interface{ plainModelIDs() }

// ProfileDefaults resolves a relay's overrides against its native defaults.
func ProfileDefaults(agent, model, effort string) (string, string) {
	if a := AgentByName(agent); a != nil {
		return resolve(a, model, effort)
	}
	return model, effort
}

// Label names a relay for people with what it runs: "Codex · GPT-6.2-Astra
// xhigh". model and effort are what it starts with ("" for the agent's own
// default, read from its configuration where it says).
func Label(agent, project, model, effort string) string {
	model, effort = ProfileDefaults(agent, model, effort)
	var parts []string
	if model != "" {
		label := ModelLabel(agent, project, model)
		if a := AgentByName(agent); a != nil {
			label = a.trimModelLabel(label, effort)
		}
		parts = append(parts, label)
	}
	if effort != "" {
		parts = append(parts, effort)
	}
	if len(parts) == 0 {
		return AgentName(agent)
	}
	return AgentName(agent) + " · " + strings.Join(parts, " ")
}
