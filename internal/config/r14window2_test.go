package config

import "testing"

// A file that names a model and the window for it, with the server's
// address in EAGENT_BASE_URL (a local server no catalog knows): the window
// the file names is the one the user chose for that model; at ecf0cf8 it
// applied. The address variable moving the route off the preset's default
// address discards it, so the window becomes 0 (read as 200k) and the
// rollover is 100k on a 32k server.
func TestR14RVFileWindowSurvivesAddressVariable(t *testing.T) {
	unsetEnv(t, routeEnvNames()...)
	t.Setenv("EAGENT_BASE_URL", "http://127.0.0.1:8/v1")
	t.Setenv("EAGENT_API_KEY_ENV", "")
	project := t.TempDir()
	writeFile(t, project, `{"model":{"model":"my-qwen-32k","context_tokens":32768}}`)
	cfg, err := Load(project, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model.ContextTokens != 32768 {
		t.Fatalf("context_tokens = %d, want the file's 32768 (rollover %d)", cfg.Model.ContextTokens, cfg.Rollover())
	}
	res := Resolve(project, "", "")
	if got := res.Effective.Model.ContextTokens; got != 32768 {
		t.Fatalf("Resolve: context_tokens = %d (source %q), want the file's 32768", got, res.Sources["/model/context_tokens"])
	}
}
