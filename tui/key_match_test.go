package tui

import (
	"fmt"
	"testing"
)

func TestParseCSIu(t *testing.T) {
	tests := []struct {
		data string
		cp   int
		mod  int
	}{
		{"\x1b[122;6u", 122, modShift | modCtrl},   // ctrl+shift+z
		{"\x1b[99;5u", 99, modCtrl},                // ctrl+c
		{"\x1b[105;6u", 105, modShift | modCtrl},   // ctrl+shift+i
		{"\x1b[97u", 97, 0},                        // a (no modifier)
		{"\x1b[97;2u", 97, modShift},               // shift+a
		{"\x1b[122;6:3u", 122, modShift | modCtrl}, // ctrl+shift+z key release (event type stripped)
		{"\x1b[97:65;2u", 97, modShift},            // shift+a with shifted key alternate
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("cp=%d_mod=%d", tt.cp, tt.mod), func(t *testing.T) {
			pk := parseCSIu(tt.data)
			if pk == nil {
				t.Fatalf("parseCSIu(%q) returned nil", tt.data)
			}
			if pk.codepoint != tt.cp {
				t.Errorf("codepoint = %d, want %d", pk.codepoint, tt.cp)
			}
			if pk.modifier != tt.mod {
				t.Errorf("modifier = %d, want %d", pk.modifier, tt.mod)
			}
		})
	}
}

func TestParseModifyOtherKeys(t *testing.T) {
	tests := []struct {
		data string
		cp   int
		mod  int
	}{
		{"\x1b[27;6;122~", 122, modShift | modCtrl}, // ctrl+shift+z
		{"\x1b[27;5;99~", 99, modCtrl},              // ctrl+c
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("cp=%d_mod=%d", tt.cp, tt.mod), func(t *testing.T) {
			pk := parseModifyOtherKeys(tt.data)
			if pk == nil {
				t.Fatalf("parseModifyOtherKeys(%q) returned nil", tt.data)
			}
			if pk.codepoint != tt.cp {
				t.Errorf("codepoint = %d, want %d", pk.codepoint, tt.cp)
			}
			if pk.modifier != tt.mod {
				t.Errorf("modifier = %d, want %d", pk.modifier, tt.mod)
			}
		})
	}
}

func TestParseModifiedArrow(t *testing.T) {
	tests := []struct {
		data string
		cp   int
		mod  int
	}{
		{"\x1b[1;6D", cpArrowLeft, modShift | modCtrl},   // ctrl+shift+left
		{"\x1b[1;6C", cpArrowRight, modShift | modCtrl},  // ctrl+shift+right
		{"\x1b[1;2A", cpArrowUp, modShift},               // shift+up
		{"\x1b[1;2B", cpArrowDown, modShift},             // shift+down
		{"\x1b[1;5D", cpArrowLeft, modCtrl},              // ctrl+left
		{"\x1b[1;3C", cpArrowRight, modAlt},              // alt+right
		{"\x1b[1;2H", cpHome, modShift},                  // shift+home
		{"\x1b[1;2F", cpEnd, modShift},                   // shift+end
		{"\x1b[1;6:3D", cpArrowLeft, modShift | modCtrl}, // with event type
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("cp=%d_mod=%d", tt.cp, tt.mod), func(t *testing.T) {
			pk := parseModifiedArrow(tt.data)
			if pk == nil {
				t.Fatalf("parseModifiedArrow(%q) returned nil", tt.data)
			}
			if pk.codepoint != tt.cp {
				t.Errorf("codepoint = %d, want %d", pk.codepoint, tt.cp)
			}
			if pk.modifier != tt.mod {
				t.Errorf("modifier = %d, want %d", pk.modifier, tt.mod)
			}
		})
	}
}

func TestParseModifiedFunc(t *testing.T) {
	tests := []struct {
		data string
		cp   int
		mod  int
	}{
		{"\x1b[5;2~", cpPageUp, modShift},   // shift+pageUp
		{"\x1b[6;2~", cpPageDown, modShift}, // shift+pageDown
		{"\x1b[3;5~", cpDelete, modCtrl},    // ctrl+delete
		{"\x1b[2;2~", cpInsert, modShift},   // shift+insert
		{"\x1b[7~", cpHome, 0},              // unmodified home
		{"\x1b[8~", cpEnd, 0},               // unmodified end
		{"\x1b[7:3~", cpHome, 0},            // unmodified home release
		{"\x1b[8;1:2~", cpEnd, 0},           // unmodified end repeat
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("cp=%d_mod=%d", tt.cp, tt.mod), func(t *testing.T) {
			pk := parseModifiedFunc(tt.data)
			if pk == nil {
				t.Fatalf("parseModifiedFunc(%q) returned nil", tt.data)
			}
			if pk.codepoint != tt.cp {
				t.Errorf("codepoint = %d, want %d", pk.codepoint, tt.cp)
			}
			if pk.modifier != tt.mod {
				t.Errorf("modifier = %d, want %d", pk.modifier, tt.mod)
			}
		})
	}
}

func TestParseKeyID(t *testing.T) {
	tests := []struct {
		input   string
		baseKey string
		mod     int
	}{
		{"ctrl+c", "c", modCtrl},
		{"ctrl+shift+z", "z", modCtrl | modShift},
		{"ctrl+shift+left", "left", modCtrl | modShift},
		{"shift+pageUp", "pageup", modShift},
		{"alt+backspace", "backspace", modAlt},
		{"ctrl+alt+x", "x", modCtrl | modAlt},
		{"a", "a", 0},
		{"escape", "escape", 0},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			baseKey, mod, ok := parseKeyID(tt.input)
			if !ok {
				t.Fatalf("parseKeyID(%q) returned !ok", tt.input)
			}
			if baseKey != tt.baseKey {
				t.Errorf("baseKey = %q, want %q", baseKey, tt.baseKey)
			}
			if mod != tt.mod {
				t.Errorf("mod = %d, want %d", mod, tt.mod)
			}
		})
	}
}

func TestMatchesKeyDynamic(t *testing.T) {
	tests := []struct {
		name  string
		data  string
		keyID string
		want  bool
	}{
		// CSI-u matches
		{"ctrl+shift+z CSI-u", "\x1b[122;6u", "ctrl+shift+z", true},
		{"ctrl+c CSI-u", "\x1b[99;5u", "ctrl+c", true},
		{"ctrl+shift+i CSI-u", "\x1b[105;6u", "ctrl+shift+i", true},

		// modifyOtherKeys matches
		{"ctrl+shift+z modifyOtherKeys", "\x1b[27;6;122~", "ctrl+shift+z", true},
		{"ctrl+c modifyOtherKeys", "\x1b[27;5;99~", "ctrl+c", true},

		// Modified arrows: the key ones for extensions!
		{"ctrl+shift+left", "\x1b[1;6D", "ctrl+shift+left", true},
		{"ctrl+shift+right", "\x1b[1;6C", "ctrl+shift+right", true},
		{"shift+up", "\x1b[1;2A", "shift+up", true},
		{"shift+down", "\x1b[1;2B", "shift+down", true},
		{"ctrl+left", "\x1b[1;5D", "ctrl+left", true},
		{"alt+right", "\x1b[1;3C", "alt+right", true},

		// Modified functional keys
		{"shift+pageUp", "\x1b[5;2~", "shift+pageUp", true},
		{"shift+pageDown", "\x1b[6;2~", "shift+pageDown", true},
		{"ctrl+delete", "\x1b[3;5~", "ctrl+delete", true},

		// Modified home/end
		{"shift+home", "\x1b[1;2H", "shift+home", true},
		{"shift+end", "\x1b[1;2F", "shift+end", true},

		// Legacy single-byte matches
		{"ctrl+c legacy", "\x03", "ctrl+c", true},
		{"ctrl+z legacy", "\x1a", "ctrl+z", true},
		{"escape legacy", "\x1b", "escape", true},
		{"enter legacy", "\r", "enter", true},
		{"tab legacy", "\t", "tab", true},
		{"shift+tab legacy", "\x1b[Z", "shift+tab", true},
		{"space legacy", " ", "space", true},
		{"backspace legacy", "\x7f", "backspace", true},
		{"alt+backspace legacy", "\x1b\x7f", "alt+backspace", true},

		// Legacy arrows
		{"up legacy", "\x1b[A", "up", true},
		{"down legacy", "\x1b[B", "down", true},
		{"left legacy", "\x1b[D", "left", true},
		{"right legacy", "\x1b[C", "right", true},
		{"left SS3", "\x1bOD", "left", true},

		// Legacy functional
		{"pageUp legacy", "\x1b[5~", "pageUp", true},
		{"pageDown legacy", "\x1b[6~", "pageDown", true},
		{"delete legacy", "\x1b[3~", "delete", true},
		{"home legacy", "\x1b[H", "home", true},
		{"end legacy", "\x1b[F", "end", true},

		// rxvt legacy shift/ctrl sequences
		{"shift+up rxvt", "\x1b[a", "shift+up", true},
		{"shift+down rxvt", "\x1b[b", "shift+down", true},
		{"ctrl+up rxvt", "\x1bOa", "ctrl+up", true},
		{"shift+pageUp rxvt", "\x1b[5$", "shift+pageUp", true},
		{"ctrl+delete rxvt", "\x1b[3^", "ctrl+delete", true},

		// Alt+letter legacy
		{"alt+x legacy", "\x1bx", "alt+x", true},
		{"alt+5 legacy", "\x1b5", "alt+5", true},

		// Alt+symbol legacy: upstream keys.ts extended the legacy alt path
		// (ESC followed by the key) from letters/digits to SYMBOL_KEYS.
		{"alt+- legacy", "\x1b-", "alt+-", true},
		{"alt+= legacy", "\x1b=", "alt+=", true},
		{"alt+/ legacy", "\x1b/", "alt+/", true},
		{"alt+backtick legacy", "\x1b`", "alt+`", true},

		// Ctrl+Alt+letter legacy
		{"ctrl+alt+c legacy", "\x1b\x03", "ctrl+alt+c", true},

		// Shift+letter → uppercase
		{"shift+a legacy", "A", "shift+a", true},

		// Single character
		{"plain a", "a", "a", true},
		{"plain 5", "5", "5", true},

		// Negative cases
		{"wrong modifier", "\x1b[1;2D", "ctrl+shift+left", false}, // shift+left ≠ ctrl+shift+left
		{"wrong key", "\x1b[1;6C", "ctrl+shift+left", false},      // ctrl+shift+right ≠ ctrl+shift+left
		{"ctrl+c ≠ ctrl+v", "\x03", "ctrl+v", false},
		{"unmodified ≠ shifted", "\x1b[A", "shift+up", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchesKeyDynamic(tt.data, tt.keyID)
			if got != tt.want {
				t.Errorf("matchesKeyDynamic(%q, %q) = %v, want %v", tt.data, tt.keyID, got, tt.want)
			}
		})
	}
}

// TestMatchesKeyID_DynamicFallback verifies that MatchesKeyID handles
// key combos not in the static map via the dynamic matcher.
func TestMatchesKeyID_DynamicFallback(t *testing.T) {
	// These are NOT in tuiKeyIDInputs: they must be resolved dynamically.
	dynamicCases := []struct {
		data  string
		keyID string
	}{
		{"\x1b[1;6D", "ctrl+shift+left"},
		{"\x1b[1;6C", "ctrl+shift+right"},
		{"\x1b[1;2A", "shift+up"},
		{"\x1b[1;2B", "shift+down"},
		{"\x1b[5;2~", "shift+pageUp"},
		{"\x1b[6;2~", "shift+pageDown"},
		{"\x1b[1;2H", "shift+home"},
		{"\x1b[1;2F", "shift+end"},
		{"\x1b[1;6A", "ctrl+shift+up"},
		{"\x1b[1;6B", "ctrl+shift+down"},
	}
	for _, tt := range dynamicCases {
		t.Run(tt.keyID, func(t *testing.T) {
			if !MatchesKeyID(tt.data, tt.keyID) {
				t.Errorf("MatchesKeyID(%q, %q) = false, want true", tt.data, tt.keyID)
			}
		})
	}
}

// TestMatchesKeyID_StaticAndDynamic verifies that keys in the static map
// still work (both paths agree).
func TestMatchesKeyID_StaticAndDynamic(t *testing.T) {
	staticCases := []struct {
		data  string
		keyID string
	}{
		{"\x03", "ctrl+c"},
		{"\x1b[99;5u", "ctrl+c"},
		{"\x1b[122;6u", "ctrl+shift+z"},
		{"\x1b", "escape"},
		{"\r", "enter"},
		{"\x1b[Z", "shift+tab"},
	}
	for _, tt := range staticCases {
		t.Run(tt.keyID, func(t *testing.T) {
			if !MatchesKeyID(tt.data, tt.keyID) {
				t.Errorf("MatchesKeyID(%q, %q) = false, want true", tt.data, tt.keyID)
			}
		})
	}
}
