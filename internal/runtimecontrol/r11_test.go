package runtimecontrol

import (
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
	"github.com/ericflo/eagent/internal/settings"
)

// Finding 17: a configuration's suggestion implies the effort the switch to
// it gives each route, for a dedicated summarizer's own route and for a
// model only the project's lists know (a registered server's, from
// live-models.json) too: choosing it with what it implies is accepted and
// gives the routes choosing it alone gives.
func TestR11SuggestionImpliesWhatTheSwitchApplies(t *testing.T) {
	together := `{"protocol":"openai-chat","base_url":"https://api.together.xyz/v1","model":"zai-org/GLM-5.3","api_key_env":"TOGETHER_API_KEY","reasoning_effort":"xhigh"}`
	loom := `{"protocol":"openai-chat","base_url":"http://127.0.0.1:8099/v1","model":"local-qwen","reasoning_effort":"xhigh"}`
	dedicated := `{"preset":"openai-high","summarizer":{"mode":"dedicated"}}`
	for _, c := range []struct{ name, file, bundle, choice string }{
		{"preset, summarizer route", `{"preset":"openai-high","summarizer":{"mode":"dedicated","model":` + together + `}}`, "", "glm"},
		{"bundle, summarizer route", dedicated, `{"summarizer":{"mode":"dedicated","model":` + together + `}}`, "bundle:b"},
		{"bundle on a registered server", dedicated, `{"model":` + loom + `}`, "bundle:b"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r3Env(t)
			project := t.TempDir()
			r10Write(t, config.File(project), c.file)
			if c.bundle != "" {
				r10Write(t, config.BundlePath(project, "b"), c.bundle)
			}
			r10Write(t, config.ServersPath(project), `{"servers":[{"id":"local-loom","label":"Loom","base_url":"http://127.0.0.1:8099/v1","protocol":"openai-chat"}]}`)
			if err := config.WriteLiveCache(project, &config.LiveCache{FetchedAt: time.Now(), Models: []config.Model{{Provider: "local-loom", ID: "local-qwen", Label: "Local Qwen", Efforts: []string{"low", "medium", "high"}, Live: true}}}); err != nil {
				t.Fatal(err)
			}
			cfg, err := config.Load(project, "")
			if err != nil {
				t.Fatal(err)
			}
			var s *control.Suggestion
			for _, sug := range settings.SessionConfigSuggestions(project, true) {
				if sug.Value == c.choice {
					s = &sug
				}
			}
			if s == nil {
				t.Fatalf("no %s suggestion", c.choice)
			}
			choose := control.Edit{Op: "set", Key: "/config", Value: c.choice}
			alone, err := r3Apply(t, project, FromConfigFor(project, cfg), choose)
			if err != nil {
				t.Fatalf("/config = %s refused: %v", c.choice, err)
			}
			edits := []control.Edit{choose}
			for k, v := range s.Implies {
				edits = append(edits, control.Edit{Op: "set", Key: k, Value: v})
			}
			with, err := r3Apply(t, project, FromConfigFor(project, cfg), edits...)
			if err != nil {
				t.Fatalf("/config = %s with what it implies refused: %v (implies %v)", c.choice, err, s.Implies)
			}
			for key, rt := range alone.Routes {
				if with.Routes[key] != rt {
					t.Errorf("%s route with what it implies %+v, alone %+v (implies %v)", key, with.Routes[key], rt, s.Implies)
				}
			}
		})
	}
}
