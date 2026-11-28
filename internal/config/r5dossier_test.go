package config

import "testing"

// Where AutoRollover's own number comes from its 20,000 floor (a window
// whose reply ceiling leaves no room for its reserve), it can sit below
// what the system prompt, the tools and the project's instructions take,
// or barely above it, and a restart then has no room to make. Held inside
// the route, the conversation always has at least a quarter of the room
// past that fixed part, and never more than three quarters.
func TestAutoRolloverWithinLeavesTheConversationRoomPastTheFixedPart(t *testing.T) {
	for window := 64_000; window <= 96_000; window += 8_000 {
		for _, reply := range []int{0, 16384, 32768, 40000} {
			for _, fixed := range []int{7300, 12000, 20000, 26000} {
				ceiling := reply
				if ceiling <= 0 {
					ceiling = 16384
				}
				room := window - ceiling - fixed
				if room <= 0 {
					continue
				}
				got := AutoRolloverWithin(window, reply, fixed)
				if got <= fixed || got < fixed+room/4 {
					t.Errorf("window %d, reply %d, fixed %d: restarts at %d, which leaves the conversation %d of the %d tokens past the fixed part", window, reply, fixed, got, got-fixed, room)
				}
				if got > fixed+room*3/4 {
					t.Errorf("window %d, reply %d, fixed %d: restarts at %d, past three quarters of the room", window, reply, fixed, got)
				}
			}
		}
	}
}
