package relay

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Other packages' tests build settings pages, which list every agent's
// models. With OfflineCatalogs they never start pi or agy, even when both
// are installed and would answer.
func TestOfflineCatalogsStartNoAgent(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	// ": >" and printf are the shell's own: nothing else is on the PATH.
	pi := "#!/bin/sh\n: > " + marker + "\nprintf 'provider model context max-out thinking images\\nlocal tiny 8K 2K no no\\n'\n"
	agy := "#!/bin/sh\n: > " + marker + "\nprintf 'tiny\\tTiny\\n'\n"
	for name, script := range map[string]string{"pi": pi, "agy": agy} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	fresh := func() {
		piCatalog.Lock()
		piCatalog.models, piCatalog.retry = nil, time.Time{}
		piCatalog.Unlock()
		agyCatalog.Lock()
		agyCatalog.models, agyCatalog.retry = nil, time.Time{}
		agyCatalog.Unlock()
	}
	fresh()
	catalogs.Lock()
	run := catalogs.run
	catalogs.Unlock()
	t.Cleanup(func() {
		catalogs.Lock()
		catalogs.run = run
		catalogs.Unlock()
		fresh()
	})

	// The stand-ins do answer when asked: the test below is not vacuous.
	if got := AgentModels("pi", ""); len(got) != 1 || got[0].ID != "local/tiny" {
		t.Fatalf("pi's list with its CLI answering = %+v", got)
	}
	if got := antigravityModels(); len(got) != 1 || got[0].ID != "tiny" {
		t.Fatalf("agy's list with its CLI answering = %+v", got)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal("neither stand-in ran: ", err)
	}

	fresh()
	OfflineCatalogs()
	if got := AgentModels("pi", ""); len(got) != 0 {
		t.Fatalf("pi's list offline = %+v", got)
	}
	if got := antigravityModels(); len(got) != 0 {
		t.Fatalf("agy's list offline = %+v", got)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("an agent's CLI was started with the catalogs offline")
	}
}
