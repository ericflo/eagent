package config

import "testing"

// The automatic threshold stays what it was on every window where it
// already fit, and on a small one lies inside what a request can take:
// past the system prompt and tools, short of the window less the reply.
func TestAutoRolloverWithinFitsSmallWindowsOnly(t *testing.T) {
	for _, c := range []struct{ window, reply int }{
		{200_000, 32768}, {200_000, 0}, {1_000_000, 32768}, {1_000_000, 128_000},
		{400_000, 128_000}, {128_000, 16384}, {80_000, 4096}, {32_768, 4096},
	} {
		if got, want := AutoRolloverWithin(c.window, c.reply, 7500), AutoRollover(c.window, c.reply); got != want {
			t.Errorf("window %d, reply %d: %d, want %d as before", c.window, c.reply, got, want)
		}
	}
	got := AutoRolloverWithin(16384, 2048, 7300)
	if got <= 7300 || got >= 16384-2048 {
		t.Fatalf("a 16,384-token window with a 2,048-token reply restarts at %d", got)
	}
	if got != 12500 {
		t.Fatalf("restarts at %d, want 12500 (three quarters of the room past the fixed part, in hundreds)", got)
	}
	// A window too small for the fixed part: nothing can be made to fit.
	if got := AutoRolloverWithin(8192, 2048, 7300); got != AutoRollover(8192, 2048) {
		t.Fatalf("a window smaller than the fixed part gives %d", got)
	}
}
