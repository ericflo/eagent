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
	os.Exit(m.Run())
}
