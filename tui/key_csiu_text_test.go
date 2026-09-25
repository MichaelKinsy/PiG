package tui

import "testing"

func TestDecodeKittyPrintable_ShiftAndText(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string // "" means ok==false (not plain text)
	}{
		{"shift+a base only (uppercase fallback) -> A", "\x1b[97;2u", "A"},
		{"shift+a with shifted alternate -> A", "\x1b[97:65;2u", "A"},
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
// could be typed.
func TestEditor_InsertsCapitalFromKittyCSIu(t *testing.T) {
	e := NewEditor()
	e.HandleInput("h")           // plain lowercase byte
	e.HandleInput("\x1b[105;2u") // Shift+i reported as CSI-u (base 105 'i' + shift)
	if got := e.Text(); got != "hI" {
		t.Fatalf("editor text = %q, want %q (Shift+i must insert capital I)", got, "hI")
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

func TestTextInput_InsertsCapitalFromModifyOtherKeys(t *testing.T) {
	ti := NewTextInput("")
	ti.HandleInput("x")
	ti.HandleInput("\x1b[27;2;89~") // Shift+y
	if got := ti.Text(); got != "xY" {
		t.Fatalf("text input = %q, want %q", got, "xY")
	}
}
