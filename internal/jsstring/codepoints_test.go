package jsstring

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCodePointsKeepLoneSurrogates(t *testing.T) {
	unit := func(u uint16) string { return FromUTF16([]uint16{u}) }
	for _, text := range []string{"", "plain \u00e9 text", unit(0xd800), unit(0xdfff), "a" + unit(0xd83d) + "\u00e9" + unit(0xde00) + "\U0001F600" + unit(0xd83d) + unit(0xd83d), unit(0xde00) + unit(0xd83d), "\U0010F800" + unit(0xd800)} {
		runes := CodePoints(text)
		if utf8.ValidString(text) && len(runes) != len([]rune(text)) {
			t.Fatalf("%q has %d code points", text, len(runes))
		}
		if got := CodePointCount(text); got != len(runes) {
			t.Fatalf("CodePointCount(%q) = %d, want %d", text, got, len(runes))
		}
		bytes := 0
		for _, r := range runes {
			if r == 0xFFFD && !strings.Contains(text, "\uFFFD") {
				t.Fatalf("%q decodes to a replacement character", text)
			}
			bytes += CodePointLen(r)
		}
		if bytes != len(text) {
			t.Fatalf("%q: the code points are %d bytes long, the text %d", text, bytes, len(text))
		}
		if got := FromCodePoints(runes); got != text {
			t.Fatalf("FromCodePoints(CodePoints(%q)) = %q", text, got)
		}
		if text != "" {
			if r, size := DecodeCodePoint(text); size == 0 || CodePointLen(r) != size {
				t.Fatalf("DecodeCodePoint(%q) = %q, %d", text, r, size)
			}
			if r, size := DecodeLastCodePoint(text); size == 0 || CodePointLen(r) != size {
				t.Fatalf("DecodeLastCodePoint(%q) = %q, %d", text, r, size)
			}
		}
	}
}

func TestCodePointsOfAnAdjacentPairOfHalvesStayTwo(t *testing.T) {
	text := FromUTF16([]uint16{0xd83d}) + FromUTF16([]uint16{0xde00})
	runes := CodePoints(text)
	if len(runes) != 2 || runes[0] != 0xd83d || runes[1] != 0xde00 || FromCodePoints(runes) != text {
		t.Fatalf("code points of %q = %U", text, runes)
	}
}

func TestCodePointStringAndAppend(t *testing.T) {
	for _, r := range []rune{'a', 'é', 0x1F600, 0xd800, 0xdfff} {
		want := FromUTF16([]uint16{uint16(r)})
		if r > 0xFFFF {
			want = string(r)
		} else if r < 0xd800 {
			want = string(r)
		}
		if got := CodePointString(r); got != want || string(AppendCodePoint(nil, r)) != want || CodePointLen(r) != len(want) {
			t.Fatalf("%U: %q, want %q", r, got, want)
		}
	}
}

func TestReplaceInvalidKeepsLoneSurrogates(t *testing.T) {
	high := FromUTF16([]uint16{0xd83d})
	for _, tc := range []struct{ in, want string }{
		{"", ""}, {"plain \u00e9", "plain \u00e9"}, {high, high}, {"a" + high + "b", "a" + high + "b"},
		{"x\xe6\x97", "x\uFFFD\uFFFD"}, {high + "\xff" + high, high + "\uFFFD" + high}, {"\xe6\x97" + high, "\uFFFD\uFFFD" + high},
	} {
		if got := ReplaceInvalid(tc.in); got != tc.want {
			t.Errorf("ReplaceInvalid(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
