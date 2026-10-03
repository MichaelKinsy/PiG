package tui

import "testing"

func TestParseOsc11BackgroundColor(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want *RgbColor
	}{
		{"rgb 16-bit white ST-terminated", "\x1b]11;rgb:ffff/ffff/ffff\x1b\\", &RgbColor{255, 255, 255}},
		{"hex6 black BEL-terminated", "\x1b]11;#000000\x07", &RgbColor{0, 0, 0}},
		{"hex12 white", "\x1b]11;#ffffffffffff\x07", &RgbColor{255, 255, 255}},
		{"rgb 16-bit mid green", "\x1b]11;rgb:0000/8080/ffff\x07", &RgbColor{0, 128, 255}},
		{"not an osc11 response", "hello\x07", nil},
		{"garbage payload", "\x1b]11;not-a-color\x07", nil},
		{"missing terminator", "\x1b]11;#000000", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseOsc11BackgroundColor(c.in)
			switch {
			case c.want == nil && got != nil:
				t.Fatalf("expected nil, got %+v", *got)
			case c.want != nil && got == nil:
				t.Fatalf("expected %+v, got nil", *c.want)
			case c.want != nil && *got != *c.want:
				t.Fatalf("got %+v, want %+v", *got, *c.want)
			}
		})
	}
}
