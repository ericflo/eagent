package web

import (
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/store"
)

// agentView is one agent profile a new session can start, with the models
// and efforts its agent takes. Model and Effort are what it runs when
// nothing is picked: the profile's, else the agent's own configuration.
type agentView struct {
	Name    string        `json:"name"`
	Agent   string        `json:"agent"`
	Who     string        `json:"who"`
	Label   string        `json:"label"`
	Model   string        `json:"model,omitempty"`
	Effort  string        `json:"effort,omitempty"`
	Default bool          `json:"default,omitempty"`
	Models  []modelChoice `json:"models"`
	Efforts []string      `json:"efforts"`
}

type modelChoice struct {
	ID     string `json:"id"`
	Label  string `json:"label,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// listAgents lists the agent profiles of the project's configuration, the
// default first, for the new-session form.
func (s *Server) listAgents(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.loadConfig("", "")
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	profiles := cfg.Relay.RelayProfiles()
	names := cfg.Relay.RelayProfileNames()
	def := defaultAgent(cfg)
	if i := slices.Index(names, def); i > 0 {
		names = append([]string{names[i]}, append(names[:i:i], names[i+1:]...)...)
	}
	out := []agentView{}
	for _, name := range names {
		p := profiles[name]
		model, effort := relay.ProfileDefaults(p.Agent, p.Model, p.Effort)
		label := p.Label
		if label == "" || p.Model == "" {
			label = relay.Label(p.Agent, s.Project, model, effort)
		}
		v := agentView{Name: name, Agent: p.Agent, Who: relay.AgentName(p.Agent), Label: label, Model: model, Effort: effort, Default: name == def, Models: []modelChoice{}, Efforts: relay.AgentEfforts(p.Agent)}
		for _, m := range relay.AgentModels(p.Agent, s.Project) {
			v.Models = append(v.Models, modelChoice{ID: m.ID, Label: m.Label, Detail: m.Detail})
		}
		if v.Efforts == nil {
			v.Efforts = []string{}
		}
		out = append(out, v)
	}
	writeJSON(w, out)
}

// defaultAgent is the profile a session starts when none is named, as the
// harness picks it: relay.default, else the first built-in agent that is
// installed, else any installed one. "" when none is.
func defaultAgent(cfg config.Config) string {
	if d := strings.TrimSpace(cfg.Relay.Default); d != "" {
		return d
	}
	profiles := cfg.Relay.RelayProfiles()
	for _, name := range append([]string{"claude", "codex", "antigravity", "pi"}, cfg.Relay.RelayProfileNames()...) {
		if p, ok := profiles[name]; ok && agentInstalled(p.Agent) {
			return name
		}
	}
	return ""
}

// agentInstalled reports whether an agent's program is on this machine;
// tests replace it.
var agentInstalled = relay.Installed

// captureScreen reads what an agent's terminal shows; tests replace it.
var captureScreen = func(host, target string, lines int) (string, error) {
	return relay.HostByName(host).Capture(target, lines)
}

// getScreen shows what one of a session's agents has on its terminal right
// now. It only reads the screen; nothing is typed.
func (s *Server) getScreen(w http.ResponseWriter, r *http.Request) {
	info, err := s.resolve(r.PathValue("id"))
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	evs, err := store.Read(info.Path)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	st := state.Replay(evs)
	rel := st.Relays[r.PathValue("name")]
	if rel == nil || rel.Target == "" {
		writeErr(w, 404, errors.New("the session has no agent by that name"))
		return
	}
	if !rel.Active() || rel.State == "exited" {
		writeErr(w, 409, errors.New(rel.Who()+" is not running, so there is no screen to show"))
		return
	}
	screen, err := captureScreen(rel.Host, rel.Target, 40)
	if err != nil {
		writeErr(w, 502, errors.New("could not read its screen: "+err.Error()))
		return
	}
	writeJSON(w, map[string]any{"name": rel.Name, "who": rel.Who(), "state": rel.State, "screen": screen})
}
