package widthx

import (
	"reflect"
	"strings"
	"testing"
)

func TestIsImageLine(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"plain text", "hello world", false},
		{"plain styled", "\x1b[1;31mred\x1b[0m", false},
		{"kitty prefix", "\x1b_Ga=T,f=100,s=10,v=20;abc\x1b\\", true},
		{"iterm2 prefix", "\x1b]1337;File=name=x.png:base64data\x07", true},
		{"kitty embedded after cursor up", "\x1b[1A\x1b_Gabc\x1b\\", true},
		{"iterm2 embedded after cursor up", "\x1b[1A\x1b]1337;File=...\x07", true},
		{"empty", "", false},
		{"lone trailing escape", "text\x1b", false},
		{"partial kitty prefix at end", "text\x1b_", false},
		{"partial iterm2 prefix", "\x1b]1337;Fil", false},
		{"kitty after many escapes", "\x1b[1m\x1b[0m\x1b\x1b]8;;\x07x\x1b_G", true},
		{"iterm2 after escape run", "\x1b\x1b\x1b]1337;File=", true},
	}
	for _, tc := range cases {
		if got := IsImageLine(tc.in); got != tc.want {
			t.Errorf("IsImageLine(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestApplyLineResets_AppendsToTextOnly(t *testing.T) {
	in := []string{
		"hello",
		"\x1b[1;31mred\x1b[0m",
		"\x1b_Gabc\x1b\\", // image line: must NOT receive suffix
		"กำ",
	}
	want := []string{
		"hello" + SegmentReset,
		"\x1b[1;31mred\x1b[0m" + SegmentReset,
		"\x1b_Gabc\x1b\\",
		"ก\u0e4d\u0e32" + SegmentReset,
	}
	got := ApplyLineResets(in)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ApplyLineResets mismatch.\ngot:  %#v\nwant: %#v", got, want)
	}
	// Input must not be mutated.
	if in[0] != "hello" {
		t.Errorf("input was mutated: in[0] = %q", in[0])
	}
}

func TestApplyLineResets_PreservesEmptySlice(t *testing.T) {
	if got := ApplyLineResets(nil); len(got) != 0 {
		t.Errorf("ApplyLineResets(nil) returned %d lines, want 0", len(got))
	}
}

// Byte-level constant parity with upstream tui.ts:425.
func TestSegmentReset_BytesMatchUpstream(t *testing.T) {
	if SegmentReset != "\x1b[0m\x1b]8;;\x07" {
		t.Errorf("SegmentReset = %q, want byte-identical match with upstream", SegmentReset)
	}
}

// TestIsImageLineMatchesContainsForm pins the single-pass scan to upstream's
// startsWith/includes form over mixed escape-heavy lines.
func TestIsImageLineMatchesContainsForm(t *testing.T) {
	pieces := []string{"a", "\x1b", "\x1b[1m", "\x1b]8;;\x07", "_G", "\x1b_", "\x1b_G", "\x1b]", "\x1b]1337;", "File=", "\x1b]1337;File=", "é", ""}
	reference := func(line string) bool {
		return strings.HasPrefix(line, kittyImagePrefix) || strings.HasPrefix(line, iTerm2ImagePrefix) ||
			strings.Contains(line, kittyImagePrefix) || strings.Contains(line, iTerm2ImagePrefix)
	}
	for _, a := range pieces {
		for _, b := range pieces {
			for _, c := range pieces {
				line := a + b + c
				if got, want := IsImageLine(line), reference(line); got != want {
					t.Fatalf("IsImageLine(%q) = %v, reference %v", line, got, want)
				}
			}
		}
	}
}
