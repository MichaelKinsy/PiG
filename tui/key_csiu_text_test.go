package tui

import "testing"

// Shift+letter with a shifted alternate matches Pi; Shift+letter without one decodes as the uppercase letter, where Pi keeps the base letter (D185).
func TestDecodeKittyPrintable_ShiftAndText(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string // "" means ok==false (not plain text)
	}{
		{"shift+a with shifted alternate -> A (as Pi)", "\x1b[97:65;2u", "A"},
		{"shift+a with a shifted alternate that is not the uppercase -> the alternate (as Pi)", "\x1b[97:98;2u", "b"},
		{"D185: shift+a base only -> A (Pi: a)", "\x1b[97;2u", "A"},
		{"D185: shift+z base only -> Z (Pi: z)", "\x1b[122;2u", "Z"},
		{"shift+A base only stays A (as Pi)", "\x1b[65;2u", "A"},
		{"plain a (report-all mode) -> a", "\x1b[97u", "a"},
		{"shift+1 with layout alternate -> !", "\x1b[49:33;2u", "!"},
		{"shift+digit no alternate -> base digit", "\x1b[49;2u", "1"},
		{"ctrl+a -> not text", "\x1b[97;5u", ""},
		{"alt+a -> not text", "\x1b[97;3u", ""},
		{"enter as CSI-u -> not text", "\x1b[13u", ""},
		{"escape as CSI-u -> not text", "\x1b[27u", ""},
		{"plain byte (not CSI-u) -> not text", "a", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := DecodeKittyPrintable(tc.in)
			if tc.want == "" {
				if ok {
					t.Errorf("DecodeKittyPrintable(%q) = (%q, true), want ok=false", tc.in, got)
				}
				return
			}
			if !ok || got != tc.want {
				t.Errorf("DecodeKittyPrintable(%q) = (%q, %v), want (%q, true)", tc.in, got, ok, tc.want)
			}
		})
	}
}

func TestDecodePrintableKey_ModifyOtherKeysShift(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"shift+a -> A", "\x1b[27;2;65~", "A"},
		{"shift+z -> Z", "\x1b[27;2;90~", "Z"},
		{"ctrl+a -> not text", "\x1b[27;5;97~", ""},
		{"alt+a -> not text", "\x1b[27;3;97~", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := DecodePrintableKey(tc.in)
			if tc.want == "" {
				if ok {
					t.Fatalf("DecodePrintableKey(%q) = (%q, true), want ok=false", tc.in, got)
				}
				return
			}
			if !ok || got != tc.want {
				t.Fatalf("DecodePrintableKey(%q) = (%q, %v), want (%q, true)", tc.in, got, ok, tc.want)
			}
		})
	}
}

// TestEditor_InsertsCapitalFromKittyCSIu guards the reported bug: in Kitty
// keyboard mode Shift+<letter> arrives as a CSI-u sequence, which the editor's
// text path dropped (it only accepted bytes >= 0x20), so no capital letters
// could be typed. The shifted-alternate form matches Pi; the base-only form
// inserts the capital where Pi inserts the base letter (D185).
func TestEditor_InsertsCapitalFromKittyCSIu(t *testing.T) {
	for _, tc := range []struct{ name, seq string }{
		{"shifted alternate (as Pi)", "\x1b[105:73;2u"},
		{"D185: base only (Pi types i)", "\x1b[105;2u"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := NewEditor()
			e.HandleInput("h") // plain lowercase byte
			e.HandleInput(tc.seq)
			if got := e.Text(); got != "hI" {
				t.Fatalf("editor text = %q, want %q (Shift+i must insert capital I)", got, "hI")
			}
		})
	}
}

func TestEditor_InsertsCapitalFromModifyOtherKeys(t *testing.T) {
	e := NewEditor()
	e.HandleInput("h")
	e.HandleInput("\x1b[27;2;73~") // Shift+i reported by xterm modifyOtherKeys
	if got := e.Text(); got != "hI" {
		t.Fatalf("editor text = %q, want %q", got, "hI")
	}
}

// input.ts decodes only Kitty CSI-u text (decodeKittyPrintable), so a modifyOtherKeys sequence is rejected as control input
// (TestInputHandleInputMatchesPi covers it against Pi); only the Editor accepts it through decodePrintableKey.
func TestTextInputIgnoresModifyOtherKeysText(t *testing.T) {
	ti := NewTextInput("")
	ti.HandleInput("x")
	ti.HandleInput("\x1b[27;2;89~") // Shift+y
	if got := ti.Text(); got != "x" {
		t.Fatalf("text input = %q, want %q", got, "x")
	}
}
