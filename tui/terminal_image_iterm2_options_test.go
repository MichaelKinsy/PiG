package tui

import (
	"strings"
	"testing"
)

// terminal-image.ts encodeITerm2(base64Data, options): every option field (width, height, name, preserveAspectRatio, inline) is reachable.
func TestEncodeITerm2OptionsCarryEveryUpstreamField(t *testing.T) {
	no, yes := false, true
	cases := []struct {
		name    string
		options ITerm2Options
		want    string
	}{
		{"no options", ITerm2Options{}, "\x1b]1337;File=inline=1;size=3:AAAA\x07"},
		{"inline false (terminal-image.ts:298)", ITerm2Options{Inline: &no}, "\x1b]1337;File=inline=0;size=3:AAAA\x07"},
		{"inline true", ITerm2Options{Inline: &yes}, "\x1b]1337;File=inline=1;size=3:AAAA\x07"},
		{"width and height (:302-303)", ITerm2Options{Width: 2, Height: "auto"}, "\x1b]1337;File=inline=1;size=3;width=2;height=auto:AAAA\x07"},
		{"name is base64 (:304-307)", ITerm2Options{Name: "x.png"}, "\x1b]1337;File=inline=1;size=3;name=eC5wbmc=:AAAA\x07"},
		{"preserveAspectRatio false (:308)", ITerm2Options{PreserveAspectRatio: &no}, "\x1b]1337;File=inline=1;size=3;preserveAspectRatio=0:AAAA\x07"},
		{"preserveAspectRatio true adds nothing", ITerm2Options{PreserveAspectRatio: &yes}, "\x1b]1337;File=inline=1;size=3:AAAA\x07"},
		{"every field in upstream order", ITerm2Options{Width: "10px", Height: 4, Name: "x.png", PreserveAspectRatio: &no, Inline: &no}, "\x1b]1337;File=inline=0;size=3;width=10px;height=4;name=eC5wbmc=;preserveAspectRatio=0:AAAA\x07"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EncodeITerm2("AAAA", tc.options); got != tc.want {
				t.Fatalf("EncodeITerm2 = %q, want %q", got, tc.want)
			}
		})
	}
}

// terminal-image.ts encodeKitty guards columns, rows and imageId with truthiness (:235-237), so any non-zero number, negative included, is emitted and only zero is absent.
func TestEncodeKittyEmitsEveryNonZeroOption(t *testing.T) {
	if got, want := EncodeKitty("AAAA", -1, -2, -3), "\x1b_Ga=T,f=100,q=2,c=-1,r=-2,i=-3;AAAA\x1b\\"; got != want {
		t.Fatalf("EncodeKitty = %q, want %q", got, want)
	}
	if got, want := EncodeKitty("AAAA", 0, 0, 0), "\x1b_Ga=T,f=100,q=2;AAAA\x1b\\"; got != want {
		t.Fatalf("EncodeKitty zero options = %q, want %q", got, want)
	}
}

// terminal-image.ts encodeKitty measures and slices base64Data with .length and .slice, which count UTF-16 units (:251-272). A payload of 2049
// astral characters is 4098 units, so Pi sends one chunk of 2048 characters (4096 units) and a last chunk of one; chunking by byte would split
// the characters. Expectation from running Pi's encodeKitty on the same input (bd3c9b82).
func TestEncodeKittyChunksByUTF16Units(t *testing.T) {
	payload := strings.Repeat("\U0001F600", 2049)
	want := "\x1b_Ga=T,f=100,q=2,c=3,r=2,i=7,m=1;" + strings.Repeat("\U0001F600", 2048) + "\x1b\\" + "\x1b_Gm=0;\U0001F600\x1b\\"
	if got := EncodeKitty(payload, 3, 2, 7); got != want {
		t.Fatalf("EncodeKitty chunks = %d bytes, want %d; head %q", len(got), len(want), got[:min(len(got), 60)])
	}
}
