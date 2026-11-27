package integration

import (
	"context"
	"testing"

	"github.com/ericflo/eagent/internal/config"
)

// The agents a phone-started session may relay to, and the one it starts
// when not told, are those of the configuration its host starts sessions on
// (an eagent server's --config), not the project's; with no host saying, the
// project's.
func TestK11TheAgentsOfferedAreTheHostsConfiguration(t *testing.T) {
	installed := agentInstalled
	defer func() { agentInstalled = installed }()
	agentInstalled = func(agent string) bool { return true }
	project := t.TempDir()
	first := func() string { return relayProfiles(project)[0]["name"].(string) }
	if got := first(); got != "claude" {
		t.Fatalf("setup: the project's default agent is %q", got)
	}
	host := config.Defaults()
	host.Relay.Default = "codex"
	restore := SetSessionStarter(func(context.Context, string, SessionRequest) (SessionStart, error) {
		return SessionStart{}, nil
	}, func(string) (config.Config, error) { return host, nil })
	if got := first(); got != "codex" {
		restore()
		t.Fatalf("under a host on another configuration the phone is offered %q first; the host starts codex", got)
	}
	restore()
	if got := first(); got != "claude" {
		t.Fatalf("after the host's starter is gone the phone is offered %q first", got)
	}
}
