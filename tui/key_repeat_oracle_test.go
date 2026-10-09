package tui

import "testing"

// Oracle: Pi 1.0.4 isKeyRepeat/isKeyRelease (packages/tui/src/keys.ts:527-575) probed over these inputs.
func TestIsKeyRepeatAndReleaseMatchPi(t *testing.T) {
	for _, tc := range []struct {
		data            string
		repeat, release bool
	}{
		{"\x1b[97;1:2u", true, false},
		{"\x1b[97;1:3u", false, true},
		{"\x1b[1;1:2A", true, false},
		{"\x1b[1;1:3F", false, true},
		{"\x1b[3;1:2~", true, false},
		{"\x1b[1;1:2B", true, false},
		{"\x1b[1;1:2C", true, false},
		{"\x1b[1;1:2D", true, false},
		{"\x1b[1;1:2H", true, false},
		{"\x1b[1;1:3B", false, true},
		{"\x1b[1;1:3C", false, true},
		{"\x1b[1;1:3D", false, true},
		{"\x1b[1;1:3H", false, true},
		{"\x1b[3;1:3~", false, true},
		{"\x1b[200~\x1b[97;1:2u", false, false},
		{"\x1b[97u", false, false},
		{"\x1b[200~90:62:2F:A5 90:62:3F:A5\x1b[201~", false, false},
		{"a:2u", true, false},
		{"\x1b[1;1:2X", false, false},
		{"", false, false},
	} {
		if got := IsKeyRepeat(tc.data); got != tc.repeat {
			t.Errorf("IsKeyRepeat(%q) = %v, want %v", tc.data, got, tc.repeat)
		}
		if got := IsKeyRelease(tc.data); got != tc.release {
			t.Errorf("IsKeyRelease(%q) = %v, want %v", tc.data, got, tc.release)
		}
	}
}
