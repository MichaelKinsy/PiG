package termgame

import "testing"

func TestParseKeyDecodesLegacyApplicationAndKitty(t *testing.T) {
	cases := []struct {
		in   string
		key  Key
		text rune
	}{
		{"\x1b[A", KeyUp, 0}, {"\x1bOA", KeyUp, 0}, {"\x1b[1;1:1A", KeyUp, 0}, {"\x1b[1;1:2A", KeyUp, 0},
		{"\x1b[B", KeyDown, 0}, {"\x1b[C", KeyRight, 0}, {"\x1b[1;2D", KeyLeft, 0},
		{"\x1b[1;1:3A", KeyNone, 0}, {"\x1b[113;1:3u", KeyNone, 0},
		{"\x1b", KeyEscape, 0}, {"\x1b[27u", KeyEscape, 0}, {"\x1b[27;1:1u", KeyEscape, 0},
		{"\r", KeyEnter, 0}, {"\x1b[13u", KeyEnter, 0},
		{" ", KeyText, ' '}, {"q", KeyText, 'q'}, {"\x1b[113u", KeyText, 'q'}, {"\x1b[32u", KeyText, ' '},
		{"\x1b[113;5u", KeyNone, 0}, {"\x03", KeyNone, 0}, {"ab", KeyNone, 0},
	}
	for _, tc := range cases {
		key, text := ParseKey(tc.in)
		if key != tc.key || text != tc.text {
			t.Errorf("ParseKey(%q) = %v %q, want %v %q", tc.in, key, text, tc.key, tc.text)
		}
	}
}

func TestViewportSubtractsTheOverlayBox(t *testing.T) {
	for _, tc := range []struct{ w, h, wantW, wantH int }{
		{80, 24, 78, 22}, {1, 1, 1, 1}, {120, 0, 118, FallbackHeight - 2},
	} {
		if w, h := Viewport(tc.w, tc.h); w != tc.wantW || h != tc.wantH {
			t.Errorf("Viewport(%d, %d) = %d, %d", tc.w, tc.h, w, h)
		}
	}
	options := Overlay("Game")
	if !options.Overlay || options.WidthFraction != 1 || options.HeightFraction != 1 || options.Title != "Game" {
		t.Fatalf("overlay options = %+v", options)
	}
}

func TestParseMouseDecodesSGRReports(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Mouse
		ok   bool
	}{
		{"\x1b[<0;12;7M", Mouse{X: 12, Y: 7}, true},
		{"\x1b[<32;13;8M", Mouse{X: 13, Y: 8, Motion: true}, true},
		{"\x1b[<0;13;8m", Mouse{X: 13, Y: 8, Release: true}, true},
		{"\x1b[<2;1;1M", Mouse{X: 1, Y: 1, Button: 2}, true},
		{"\x1b[A", Mouse{}, false},
		{"\x1b[<0;x;1M", Mouse{}, false},
		{"\x1b[<0;1M", Mouse{}, false},
	} {
		got, ok := ParseMouse(tc.in)
		if ok != tc.ok || got != tc.want {
			t.Errorf("ParseMouse(%q) = %+v %v, want %+v %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
	if x, y := ViewCell(Mouse{X: 2, Y: 2}); x != 0 || y != 0 {
		t.Fatalf("the first content cell maps to %d,%d", x, y)
	}
}
