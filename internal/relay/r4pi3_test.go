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
	// The hooks see the hand-over at the extension's first look (1 s); the
	// typed run must start before the login check after them ends, and
	// last past it. Sped up, the stand-in's times give a second between
	// each. The extension as shipped runs the plain case on times close
	// together, so it takes three seconds rather than six: this is the
	// one hand-over test that runs the extension's real timers.
	const fast, real = `[[2000, "type", "a typed message"]]`, `[[1200, "type", "a typed message"]]`
	for _, tc := range []struct {
		name, text string
		speed      int
		run        time.Duration
		steps      string
		env        []string
	}{
		{"plain, as shipped", held, piReal, 3 * time.Second, real, []string{"PREFLIGHT_MS=600", "RUN_EACH_MS=800"}},
		{"plain", held, piFast, 6 * time.Second, fast, []string{"PREFLIGHT_MS=2000", "RUN_EACH_MS=2500"}},
		{"tabs", "run this:\n\tgo test ./...\n\tgo vet ./...", piFast, 6 * time.Second, fast, []string{"PREFLIGHT_MS=2000", "RUN_EACH_MS=2500"}},
		{"carriage returns", "first line\r\nsecond line\rthird", piFast, 6 * time.Second, fast, []string{"PREFLIGHT_MS=2000", "RUN_EACH_MS=2500"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			list, wait, _ := runPiPromptPathWith(t, tc.speed, tc.text, tc.run, tc.steps, append(tc.env, "ABORT_QUEUED=1")...)
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
