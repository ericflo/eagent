package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// loadedFlat is what a project with this file loads to, as JSON pointers,
// without the notes about older keys.
func loadedFlat(t *testing.T, project, bundle string) map[string]string {
	t.Helper()
	cfg, err := LoadBundle(project, "", bundle)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return flatten(cfg)
}

// Files eagent wrote before (from main, and from this branch before zero
// values were spelled out) load to exactly what they loaded to then. The
// loaded values are kept beside each sample, as written by the code that
// wrote it.
func TestFilesWrittenBeforeKeepLoadingToTheSameValues(t *testing.T) {
	update := os.Getenv("EAGENT_UPDATE_WRITTEN") != ""
	noEagentEnv(t)
	samples, err := filepath.Glob("testdata/written/*.json")
	if err != nil || len(samples) == 0 {
		t.Fatalf("no samples: %v", err)
	}
	for _, sample := range samples {
		if strings.HasSuffix(sample, ".loaded.json") {
			continue
		}
		raw, err := os.ReadFile(sample)
		if err != nil {
			t.Fatal(err)
		}
		project, bundle := t.TempDir(), ""
		if strings.Contains(sample, "-bundle-") {
			bundle = "mine"
			if err := os.MkdirAll(BundlesDir(project), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(BundlePath(project, bundle), raw, 0o644); err != nil {
				t.Fatal(err)
			}
		} else {
			writeConfig(t, project, string(raw))
		}
		got := loadedFlat(t, project, bundle)
		golden := strings.TrimSuffix(sample, ".json") + ".loaded.json"
		if update {
			out, _ := json.MarshalIndent(got, "", "  ")
			if err := os.WriteFile(golden, append(out, '\n'), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		wantRaw, err := os.ReadFile(golden)
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]string{}
		if err := json.Unmarshal(wantRaw, &want); err != nil {
			t.Fatal(err)
		}
		var diffs []string
		for k, v := range want {
			if got[k] != v {
				diffs = append(diffs, k+": was "+v+", now "+got[k])
			}
		}
		for k, v := range got {
			if _, ok := want[k]; !ok {
				diffs = append(diffs, k+": new, "+v)
			}
		}
		sort.Strings(diffs)
		if len(diffs) > 0 {
			t.Errorf("%s loads differently:\n  %s", filepath.Base(sample), strings.Join(diffs, "\n  "))
		}
	}
}
