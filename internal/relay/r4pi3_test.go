package relay

import (
	"strings"
	"testing"
	"time"
)

// pi's editor keeps line ends as "\n" and tabs as four spaces, so a
// hand-over with a tab in it does not sit there letter for letter. pi is
// idle when the hand-over goes through its input hook; a typed message
// starts its run first, pi queues the hand-over into that run, and the
// run is interrupted: the hand-over is in the editor, the person's now,
// and is not handed over again.
func TestPiHandOverWithTabsPutBackInTheEditorIsNotSentAgain(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, text string }{
		{"plain", held},
		{"tabs", "run this:\n\tgo test ./...\n\tgo vet ./..."},
		{"carriage returns", "first line\r\nsecond line\rthird"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			list, wait := runPiPromptPathWith(t, tc.text, 3500*time.Millisecond, `[[1100, "type", "a typed message"]]`,
				"PREFLIGHT_MS=400", "RUN_EACH_MS=800", "ABORT_QUEUED=1")
			r := wait()
			if !strings.Contains(r.out, `"editor":`) || r.count("ran", "a typed message") != 1 {
				t.Fatalf("the scenario did not run as meant (the hand-over queued into the typed run, then put in the editor):\n%s", r.out)
			}
			if r.count("ran", tc.text) != 0 || len(list()) != 0 {
				t.Fatalf("the hand-over pi put back in its editor is still claimed (%v), to be handed over again:\n%s", list(), r.out)
			}
		})
	}
}
