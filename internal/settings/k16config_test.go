package settings

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
	"github.com/ericflo/eagent/internal/relay"
)

// A preset chosen in the settings runs its whole route or is refused. Where
// a part of the route stays that the change cannot reach (the connector's
// grant does not cover the address, a saved preset sets it, a variable
// sets it), the preset's model would run at that address: nothing is
// written, and the refusal says what stays and why, as `eagent presets use`
// does. (The change was accepted, and the settings named a preset while
// OpenAI's model ran at DeepInfra.) Unsetting the preset goes by the same
// rule for the defaults.
func TestK16APresetThatWouldNotRunAsItIsIsRefused(t *testing.T) {
	relay.OfflineCatalogs()
	const deepinfra = "https://api.deepinfra.com/v1/openai"
	for _, c := range []struct {
		name, file, bundle, variable string
		narrow                       bool
		edit                         control.Edit
		says                         []string
	}{
		{name: "the connector's grant does not cover the address", narrow: true,
			file: `{"model":{"model":"zai-org/GLM-5.2","base_url":"` + deepinfra + `","api_key_env":"DEEPINFRA_API_KEY"}}`,
			edit: control.Edit{Op: "set", Key: "/preset", Value: "openai-high"},
			says: []string{"base_url stays " + deepinfra, "api_key_env stays DEEPINFRA_API_KEY", "no grant for credential_reference", "the preset openai-high"}},
		{name: "a saved preset sets the address",
			file:   `{"default_config":"mine","model":{"model":"zai-org/GLM-5.2"}}`,
			bundle: `{"model":{"base_url":"` + deepinfra + `","api_key_env":"DEEPINFRA_API_KEY","protocol":"openai-chat"}}`,
			edit:   control.Edit{Op: "set", Key: "/preset", Value: "openai-high"},
			says:   []string{"base_url stays " + deepinfra, `the saved preset "mine" sets it`, "protocol stays openai-chat", "the preset openai-high"}},
		{name: "a variable sets the address", variable: "https://api.together.xyz/v1",
			file: `{"model":{"model":"zai-org/GLM-5.2"}}`,
			edit: control.Edit{Op: "set", Key: "/preset", Value: "openai-high"},
			says: []string{"base_url stays https://api.together.xyz/v1 (EAGENT_BASE_URL sets it)", "the preset openai-high"}},
		{name: "unset, and the grant does not cover the address", narrow: true,
			file: `{"preset":"openai-high","model":{"base_url":"` + deepinfra + `","api_key_env":"DEEPINFRA_API_KEY"}}`,
			edit: control.Edit{Op: "unset", Key: "/preset"},
			says: []string{"base_url stays " + deepinfra, "no grant for credential_reference", "the defaults ("}},
	} {
		t.Run(c.name, func(t *testing.T) {
			clearRouteEnv(t)
			t.Setenv("EAGENT_FINALECHAT", "")
			for _, key := range []string{"OPENAI_API_KEY", "DEEPINFRA_API_KEY", "TOGETHER_API_KEY"} {
				t.Setenv(key, "x")
			}
			if c.variable != "" {
				t.Setenv("EAGENT_BASE_URL", c.variable)
			}
			project := t.TempDir()
			writeProjectFile(t, project, c.file)
			if c.bundle != "" {
				if err := os.MkdirAll(config.BundlesDir(project), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(config.BundlePath(project, "mine"), []byte(c.bundle), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			s := &Service{Project: project}
			grant := s.Grant()
			if c.narrow {
				grant.Classes = []string{"preference"}
			}
			view, err := s.RemoteSnapshot(grant)
			if err != nil {
				t.Fatal(err)
			}
			p := control.Proposal{Operation: "settings.apply", SchemaVersion: SchemaVersion, ExpectedVersion: view.Snapshot.Version, Edits: []control.Edit{c.edit}}
			_, _, err = s.Apply(context.Background(), p, grant)
			written, _ := os.ReadFile(config.File(project))
			if err == nil {
				got, _ := config.Load(project, "")
				t.Fatalf("accepted, and runs %s @ %s (%s, $%s)\nfile: %s", got.Model.Model, got.Model.BaseURL, got.Model.Protocol, got.Model.APIKeyEnv, written)
			}
			for _, want := range append(c.says, "nothing was changed") {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not say %q: %v", want, err)
				}
			}
			if string(written) != c.file {
				t.Errorf("the file was changed: %s", written)
			}
		})
	}
}

// A saved preset chosen or unset in the settings (/default_config) is no
// pick of a built-in preset, though it can bring one with it: the file's
// own model and address are not that change's to remove. (They went with
// it, and the saved preset's model ran in place of the file's.)
func TestK16ASavedPresetChosenLeavesTheFilesRoute(t *testing.T) {
	relay.OfflineCatalogs()
	clearRouteEnv(t)
	t.Setenv("EAGENT_FINALECHAT", "")
	for _, key := range []string{"OPENAI_API_KEY", "DEEPINFRA_API_KEY"} {
		t.Setenv(key, "x")
	}
	const file = `{"model":{"model":"zai-org/GLM-5.2","base_url":"https://api.deepinfra.com/v1/openai","api_key_env":"DEEPINFRA_API_KEY"}}`
	project := t.TempDir()
	writeProjectFile(t, project, file)
	if err := os.MkdirAll(config.BundlesDir(project), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.BundlePath(project, "mine"), []byte(`{"preset":"openai-high","persona":"terse"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	read := func() map[string]any {
		t.Helper()
		raw, _ := os.ReadFile(config.File(project))
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("%v: %s", err, raw)
		}
		return m
	}
	was := read()
	s := &Service{Project: project}
	if err := tryApply(t, s, control.Edit{Op: "set", Key: "/default_config", Value: "mine"}); err != nil {
		t.Fatal(err)
	}
	if got := read(); got["default_config"] != "mine" || !reflect.DeepEqual(got["model"], was["model"]) {
		t.Fatalf("choosing the saved preset wrote %v; want default_config beside the file's own route %v", got, was["model"])
	}
	if err := tryApply(t, s, control.Edit{Op: "unset", Key: "/default_config"}); err != nil {
		t.Fatal(err)
	}
	if got := read(); !reflect.DeepEqual(got, was) {
		t.Fatalf("unsetting it left %v; want the file as it was, %v", got, was)
	}
}

// One save that changes the registered servers and chooses a preset that
// would not run as it is: the choice is refused before a server is touched,
// so its "nothing was changed" holds. (The servers were written first, and
// the refusal came after: one registered server had become none, with no
// undo for a refused save.) The same save with a preset that runs writes
// both.
func TestK16APresetRefusedBesideServerEditsLeavesTheServers(t *testing.T) {
	relay.OfflineCatalogs()
	clearRouteEnv(t)
	t.Setenv("EAGENT_FINALECHAT", "")
	for _, key := range []string{"OPENAI_API_KEY", "TOGETHER_API_KEY"} {
		t.Setenv(key, "x")
	}
	const file = `{"model":{"model":"zai-org/GLM-5.2"}}`
	const servers = `[{"id":"box","label":"box","base_url":"http://127.0.0.1:9","protocol":"openai-chat"}]`
	edits := []control.Edit{{Op: "unset", Key: "/servers"}, {Op: "set", Key: "/preset", Value: "openai-high"}}
	set := func(t *testing.T) (*Service, string) {
		project := t.TempDir()
		writeProjectFile(t, project, file)
		if err := os.WriteFile(config.ServersPath(project), []byte(servers), 0o644); err != nil {
			t.Fatal(err)
		}
		return &Service{Project: project}, project
	}
	t.Run("refused", func(t *testing.T) {
		t.Setenv("EAGENT_BASE_URL", "https://api.together.xyz/v1")
		s, project := set(t)
		err := tryApply(t, s, edits...)
		if err == nil || !strings.Contains(err.Error(), "EAGENT_BASE_URL sets it") || !strings.Contains(err.Error(), "nothing was changed") {
			t.Fatalf("got %v; want the choice refused for the variable", err)
		}
		if got, _ := config.LoadServers(project); len(got) != 1 {
			t.Fatalf("the refusal says nothing was changed, and the registered servers went from 1 to %d", len(got))
		}
		if raw, _ := os.ReadFile(config.File(project)); string(raw) != file {
			t.Fatalf("the refused save wrote the file: %s", raw)
		}
	})
	t.Run("runs", func(t *testing.T) {
		s, project := set(t)
		if err := tryApply(t, s, edits...); err != nil {
			t.Fatalf("a preset that runs was refused beside the server edit: %v", err)
		}
		if got, _ := config.LoadServers(project); len(got) != 0 {
			t.Fatalf("the servers were not written: %d left", len(got))
		}
		if cfg, err := config.Load(project, ""); err != nil || cfg.Preset != "openai-high" {
			t.Fatalf("preset %q, %v; want openai-high", cfg.Preset, err)
		}
	})
}
