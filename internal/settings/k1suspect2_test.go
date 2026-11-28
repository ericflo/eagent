package settings

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ericflo/eagent/internal/config"
)

// The reader's second suspicion of d7f7b75, not a fault: "Save as preset"
// under EAGENT_BASE_URL saves the setup in force, the variable's address
// with it (the editor sends its whole form, as on main), and now that
// server's window with that address; at f36e19c the bundle had the local
// address with Together AI's 200000.
func TestK1ABundleSavedUnderAnAddressVariableHasThatServersWindow(t *testing.T) {
	project, url := rv15b(t)
	cfg := config.Resolve(project, "", "").Effective
	raw, _ := json.Marshal(cfg)
	s := &Service{Project: project}
	if _, err := s.SaveBundle(context.Background(), BundleRequest{Name: "mine", Config: raw}); err != nil {
		t.Fatal(err)
	}
	got, err := config.BundleAlone(project, "mine")
	if err != nil || got.Model.BaseURL != url || got.Model.ContextTokens != 32_768 {
		t.Fatalf("bundle: %s at %d (err %v)", got.Model.BaseURL, got.Model.ContextTokens, err)
	}
}
