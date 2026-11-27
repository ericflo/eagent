package settings

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/config"
)

// A refusal to delete a saved preset in force names what keeps it, and only
// default_config, the one thing a settings edit can change, is answered
// with "select a different default".
func TestK13EachRefusalNamesWhatKeepsThePreset(t *testing.T) {
	for _, c := range []struct {
		name, file, variable string
		service              Service
		host                 bool
		want                 string
	}{
		{name: "default_config", file: `{"default_config":"work"}`, want: "select a different default"},
		{name: "the server's --config", file: `{"default_config":"other"}`, service: Service{Bundle: "work"}, want: "--config"},
		{name: "EAGENT_PRESET", file: `{}`, variable: "EAGENT_PRESET", want: "EAGENT_PRESET"},
		{name: "EAGENT_CONFIG", file: `{}`, variable: "EAGENT_CONFIG", want: "EAGENT_CONFIG"},
		{name: "the host's --config", file: `{"default_config":"other"}`, host: true, want: "--config"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("EAGENT_PRESET", "")
			t.Setenv("EAGENT_CONFIG", "")
			t.Setenv("EAGENT_FINALECHAT", "")
			project := t.TempDir()
			for _, name := range []string{"work", "other"} {
				if _, err := config.SaveBundle(project, name, "", config.Defaults()); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(config.File(project), []byte(c.file), 0o644); err != nil {
				t.Fatal(err)
			}
			if c.variable != "" {
				t.Setenv(c.variable, "work")
			}
			if c.host {
				previous := SetHost(func(p string) (config.Config, error) { return config.LoadBundle(p, "", "work") })
				defer SetHost(previous)
			}
			s := c.service
			s.Project = project
			err := s.DeleteBundle(context.Background(), "work")
			if err == nil {
				t.Fatal("deleted the saved preset in force")
			}
			msg := err.Error()
			if !strings.Contains(msg, c.want) || c.want != "select a different default" && strings.Contains(msg, "select a different default") {
				t.Fatalf("the refusal says %q; it should name %s", msg, c.want)
			}
		})
	}
}
