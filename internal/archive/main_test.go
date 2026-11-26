package archive

import (
	"os"
	"testing"

	"github.com/ericflo/eagent/internal/relay"
)

// Tests never start the agents installed on this machine to list their
// models.
func TestMain(m *testing.M) {
	relay.OfflineCatalogs()
	os.Exit(m.Run())
}
