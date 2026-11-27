package settings

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/relay"
)

// `eagent serve --config openai-high` (a built-in named as the bundle,
// which outranks EAGENT_CONFIG) in a shell where EAGENT_CONFIG=vc: the
// server starts and resumes its sessions on openai-high, never on vc. The
// web editor's delete of vc is refused as "in force: EAGENT_CONFIG names
// it", which the server does not follow. On main the same delete went
// through (it refused only the active bundle or default_config).
func TestSR9WebDeleteUnderBuiltinConfigFlagIsNotRefusedForEAGENTCONFIG(t *testing.T) {
	relay.OfflineCatalogs()
	t.Setenv("EAGENT_FINALECHAT", "")
	t.Setenv("EAGENT_PRESET", "")
	project := t.TempDir()
	for _, n := range []string{"vc", "other"} {
		if _, err := config.SaveBundle(project, n, "", config.Defaults()); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("EAGENT_CONFIG", "vc")
	host := func(p string) (config.Config, error) { return config.LoadBundle(p, "", "openai-high") }
	previous := SetHost(host)
	defer SetHost(previous)
	runs, err := host(project)
	if err != nil || runs.Name == "vc" {
		t.Fatalf("setup: the server runs %q (%v)", runs.Name, err)
	}
	web := &Service{Project: project, Bundle: "openai-high"}
	if err := web.DeleteBundle(context.Background(), "vc"); err != nil {
		t.Fatalf("web delete of vc, which the server (--config openai-high, preset %q, bundle %q) does not run: %v", runs.Preset, runs.Name, err)
	}
	if _, err := os.Stat(config.BundlePath(project, "vc")); !os.IsNotExist(err) {
		t.Fatalf("vc still on disk: %v", err)
	}
}

// The phone's sibling: `eagent serve --preset vp` (a saved preset, which
// outranks EAGENT_CONFIG) with EAGENT_CONFIG=vc. The phone's sessions start
// on the host's configuration (vp); the phone's service has no flags, so
// its delete of vc is refused as "in force: EAGENT_CONFIG names it". On
// main the phone's check (active bundle or default_config) let it go.
func TestSR9PhoneDeleteUnderSavedPresetFlagIsNotRefusedForEAGENTCONFIG(t *testing.T) {
	relay.OfflineCatalogs()
	t.Setenv("EAGENT_FINALECHAT", "")
	t.Setenv("EAGENT_PRESET", "")
	project := t.TempDir()
	for _, n := range []string{"vc", "vp"} {
		if _, err := config.SaveBundle(project, n, "", config.Defaults()); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("EAGENT_CONFIG", "vc")
	host := func(p string) (config.Config, error) { return config.LoadBundle(p, "vp", "") }
	previous := SetHost(host)
	defer SetHost(previous)
	if runs, err := host(project); err != nil || runs.Name != "vp" {
		t.Fatalf("setup: the server runs %q (%v)", runs.Name, err)
	}
	phone := &Service{Project: project}
	if err := phone.DeleteBundle(context.Background(), "vc"); err != nil {
		t.Fatalf("phone delete of vc, which the server (--preset vp) does not run: %v", err)
	}
}

// A server started with no flags under EAGENT_PRESET=vp runs vp because of
// the variable: the phone's refusal names the variable, never a --config or
// --preset the server was not given.
func TestK14TheHostsReasonIsTheLayerThatLaidIt(t *testing.T) {
	relay.OfflineCatalogs()
	t.Setenv("EAGENT_FINALECHAT", "")
	t.Setenv("EAGENT_CONFIG", "")
	project := t.TempDir()
	if _, err := config.SaveBundle(project, "vp", "", config.Defaults()); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EAGENT_PRESET", "vp")
	previous := SetHost(func(p string) (config.Config, error) { return config.LoadBundle(p, "", "") })
	defer SetHost(previous)
	err := (&Service{Project: project}).DeleteBundle(context.Background(), "vp")
	if err == nil || !strings.Contains(err.Error(), "EAGENT_PRESET") || strings.Contains(err.Error(), "--config") {
		t.Fatalf("the phone's refusal to delete vp, which the server runs because EAGENT_PRESET names it: %v", err)
	}
}
