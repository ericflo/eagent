package integration

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/ericflo/eagent/internal/config"
)

// TestMain keeps the connector's model-list refresh from asking real
// providers for their models with the developer's keys.
func TestMain(m *testing.M) {
	config.SetLiveProbe(func(context.Context, config.Provider) ([]config.Model, error) {
		return nil, errors.New("providers are offline in tests")
	})
	// Sessions started here run on scripted models; their own summary
	// call (harness/summary.go) is covered in the harness package.
	os.Setenv("EAGENT_SUMMARIES", "off")
	os.Exit(m.Run())
}
