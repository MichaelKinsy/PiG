package tui

import "testing"

func TestDecodeKittyPrintable_KeypadFunctionalKeys(t *testing.T) {
	cases := map[string]string{
		"\x1b[57399u": "0",
		"\x1b[57400u": "1",
		"\x1b[57409u": ".",
		"\x1b[57410u": "/",
		"\x1b[57411u": "*",
		"\x1b[57412u": "-",
		"\x1b[57413u": "+",
		"\x1b[57415u": "=",
		"\x1b[57416u": ",",
	}
	for input, want := range cases {
		got, ok := DecodeKittyPrintable(input)
		if !ok || got != want {
			t.Fatalf("DecodeKittyPrintable(%q) = (%q,%v), want (%q,true)", input, got, ok, want)
		}
	}
	if got, ok := DecodeKittyPrintable("\x1b[57417u"); ok || got != "" {
		t.Fatalf("DecodeKittyPrintable(left keypad arrow) = (%q,%v), want (\"\",false)", got, ok)
	}
}

func TestDecodePrintableKey_ModifyOtherKeys(t *testing.T) {
	cases := []struct {
		input string
		want  string
		ok    bool
	}{
		{"\x1b[27;2;69~", "E", true},
		{"\x1b[27;2;196~", "Ä", true},
		{"\x1b[27;2;32~", " ", true},
		{"\x1b[27;2;13~", "", false},
		{"\x1b[27;6;69~", "", false},
	}
	for _, tc := range cases {
		got, ok := DecodePrintableKey(tc.input)
		if got != tc.want || ok != tc.ok {
			t.Fatalf("DecodePrintableKey(%q) = (%q,%v), want (%q,%v)", tc.input, got, ok, tc.want, tc.ok)
		}
	}
}

func TestTextInputDecodesKittyPrintable(t *testing.T) {
	ti := NewTextInput("")
	ti.HandleInput("\x1b[57399u")
	ti.HandleInput("\x1b[57409u")
	if got := ti.Text(); got != "0." {
		t.Fatalf("Text() = %q want %q", got, "0.")
	}
}

// wantsReleaseComponent is a component that opts into key releases, like
// upstream's space-invaders and doom-overlay examples.
type wantsReleaseComponent struct{ wants bool }

func (w *wantsReleaseComponent) Render(int) []string   { return nil }
func (w *wantsReleaseComponent) Invalidate()           {}
func (w *wantsReleaseComponent) WantsKeyRelease() bool { return w.wants }

// plainComponent implements no opt-in, so it must never see a release.
type plainComponent struct{}

func (plainComponent) Render(int) []string { return nil }
func (plainComponent) Invalidate()         {}

// TestShouldDeliverKey pins the one rule governing focused-component key
// delivery: releases are dropped unless the component opts in, and presses are
// always delivered. Mirrors upstream tui.ts:887.
func TestShouldDeliverKey(t *testing.T) {
	const (
		pressDown   = "\x1b[1;1:1B"
		releaseDown = "\x1b[1;1:3B"
		legacyDown  = "\x1b[B"
		paste       = "\x1b[200~:3B\x1b[201~"
	)
	tests := []struct {
		name      string
		component Component
		data      string
		want      bool
	}{
		{"press to plain component", plainComponent{}, pressDown, true},
		{"release to plain component", plainComponent{}, releaseDown, false},
		{"legacy press to plain component", plainComponent{}, legacyDown, true},
		{"release to opted-in component", &wantsReleaseComponent{wants: true}, releaseDown, true},
		{"release to opted-out component", &wantsReleaseComponent{wants: false}, releaseDown, false},
		{"press to opted-in component", &wantsReleaseComponent{wants: true}, pressDown, true},
		// Bracketed paste content is never a release, even when it contains
		// the byte pattern, so pasted text always reaches the component.
		{"paste containing release bytes", plainComponent{}, paste, true},
		// A nil component cannot opt in; releases must still be dropped
		// rather than panicking.
		{"release with nil component", nil, releaseDown, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ShouldDeliverKey(tc.component, tc.data); got != tc.want {
				t.Errorf("ShouldDeliverKey(%q) = %t, want %t", tc.data, got, tc.want)
			}
		})
	}
}
