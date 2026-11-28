package settings

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/ericflo/eagent/internal/config"
)

// Keys no version of eagent had, inside an object of the three-actor
// design: main refused them (its routes had no such field), 12aec43 refused
// the whole object, and the change says "a key no version of eagent had is
// still refused".
func TestRV9ZUnknownKeysInLegacyObjectsRefused(t *testing.T) {
	s := &Service{Project: t.TempDir()}
	for _, raw := range []string{
		`{"narrator": {"bogus": 1}}`,
		`{"task": {"modle": "x"}}`,
		`{"model": {"model": "zai-org/GLM-5.3"}, "orchestrator": {"bogus": 1}}`,
		`{"orchestrator": 5}`,
	} {
		old := config.Defaults()
		dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
		dec.DisallowUnknownFields()
		oldErr := dec.Decode(&old)
		_, err := s.BundleConfig(BundleRequest{Name: "x", Config: json.RawMessage(raw)})
		if err == nil {
			t.Errorf("%s: accepted (12aec43: %v)", raw, oldErr)
		}
	}
}
