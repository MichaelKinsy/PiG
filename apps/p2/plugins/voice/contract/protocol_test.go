package contract

import (
	"bytes"
	"testing"
)

// The server's room names and the frames its bridge and tests read are Go's (protocol.ts's roomName and parseRoomFrame); the
// browser reads them with Pi's own, which voice.web.test.ts runs.
func TestVoiceRoomFrames(t *testing.T) {
	t.Run("splits a binary frame into kind, ID and PCM past the pad byte, copied to a buffer of its own", func(t *testing.T) {
		// Kind 1, ID "a-1": 2 + 3 bytes is odd, so one pad byte; the PCM starts at 6.
		source := []byte{1, 3, 0x61, 0x2d, 0x31, 0, 1, 2, 3, 4}
		odd := ParseRoomFrame(source)
		if odd == nil || odd.Kind != 1 || odd.ID != "a-1" || !bytes.Equal(odd.PCM, []byte{1, 2, 3, 4}) {
			t.Fatalf("odd = %+v", odd)
		}
		// Kind 2, ID "ab": 2 + 2 bytes is even, no pad.
		even := ParseRoomFrame([]byte{2, 2, 0x61, 0x62, 5, 6})
		if even == nil || even.Kind != 2 || even.ID != "ab" || !bytes.Equal(even.PCM, []byte{5, 6}) {
			t.Fatalf("even = %+v", even)
		}
		source[6] = 99
		if odd.PCM[0] != 1 {
			t.Error("the PCM shares the frame's buffer")
		}
		if ParseRoomFrame([]byte{1, 9, 1}) != nil {
			t.Error("a frame too short for its ID parsed")
		}
		if got := RoomName("s1", 1); got != "p2-s1-1" {
			t.Errorf("RoomName = %q", got)
		}
		if got := RoomName("bad id", 1); got != "" {
			t.Errorf("RoomName of a bad ID = %q", got)
		}
	})
}
