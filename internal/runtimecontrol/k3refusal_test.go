package runtimecontrol

import (
	"errors"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
)

// A change the session's Apply refuses before it records anything
// (Refusal) is reported rejected, with its message; any other failure of
// Apply may have left it recorded, and stays unknown.
func TestK3ARefusalIsRejectedAndAFailedWriteUnknown(t *testing.T) {
	r14Env(t)
	project := t.TempDir()
	cfg, err := config.Load(project, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		err    error
		status string
	}{
		{Refusal{Err: errors.New("Codex is being replaced by Claude; change the model once that is up")}, "rejected"},
		{errors.New("the settings event was not durably recorded"), "unknown"},
	} {
		c, err := New(project, "s", FromConfigFor(project, cfg), func(Request, Values, map[string]any) error { return tc.err })
		if err != nil {
			t.Fatal(err)
		}
		q := r14Request(t, c, control.Edit{Op: "set", Key: "/tick_seconds", Value: float64(120)})
		out := r14Process(t, c, q)
		msg, _ := out.Result["message"].(string)
		if out.Status != tc.status || tc.status == "rejected" && msg != tc.err.Error() {
			t.Errorf("Apply's %q: outcome %s %q, want %s", tc.err, out.Status, msg, tc.status)
		}
		c.Close()
	}
}
