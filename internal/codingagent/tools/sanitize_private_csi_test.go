package tools

import "testing"

// Ports packages/coding-agent/test/ansi-utils.test.ts:111-123 (Pi b5fc6dc8, issue #11377): a private prefix after a CSI introducer is
// part of the sequence; after a bare ESC it stays a final byte.
func TestStripANSIPrivateParameterCSI(t *testing.T) {
	t.Run("strips private-parameter CSI sequences with their parameters", func(t *testing.T) {
		for _, tc := range []struct{ in, want string }{
			{"\x1b[<35;59;1Mstep 1", "step 1"},
			{"a\x1b[<0;10;20mb", "ab"},
			{"a\x1b[>0;276;0cb", "ab"},
			{"a\x1b[>cb", "ab"},
			{"a\x1b[=5hb", "ab"},
			{"a\u009b<35;59;1Mb", "ab"},
		} {
			if got := string(StripANSI([]byte(tc.in))); got != tc.want {
				t.Errorf("StripANSI(%q) = %q, want %q", tc.in, got, tc.want)
			}
		}
	})
	t.Run("keeps digits after bare ESC keypad-mode sequences", func(t *testing.T) {
		for _, tc := range []struct{ in, want string }{
			{"\x1b[?1h\x1b=123 tests passed", "123 tests passed"},
			{"\x1b[?1l\x1b>42 files", "42 files"},
			{"\x1b<10 items", "10 items"},
		} {
			if got := string(StripANSI([]byte(tc.in))); got != tc.want {
				t.Errorf("StripANSI(%q) = %q, want %q", tc.in, got, tc.want)
			}
		}
	})
}

// Ports packages/coding-agent/test/ansi-utils.test.ts:125-134.
func TestSplitIncompleteAnsiSuffixPrivateParameterCSI(t *testing.T) {
	for _, tc := range []struct{ in, complete, pending string }{
		{"x\x1b[<35;59", "x", "\x1b[<35;59"},
		{"x\x1b=", "x\x1b=", ""},
	} {
		complete, pending := SplitIncompleteAnsiSuffix(tc.in)
		if complete != tc.complete || pending != tc.pending {
			t.Errorf("SplitIncompleteAnsiSuffix(%q) = (%q, %q), want (%q, %q)", tc.in, complete, pending, tc.complete, tc.pending)
		}
	}
}
