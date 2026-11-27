package tui

import (
	"strings"
	"testing"
)

func TestHyperlinkURLLengthCapUTF16(t *testing.T) {
	prefix := "https://example.com/"
	atLimit := prefix + strings.Repeat("a", maxHyperlinkURLLength-len(prefix))
	if got := Hyperlink("ok", atLimit); !strings.Contains(got, "\x1b]8;;") {
		t.Fatalf("URL of exact length %d should be linked", len(atLimit))
	}
	if got := Hyperlink("ok", atLimit+"a"); got != "ok" {
		t.Fatalf("URL over 8192 UTF-16 units should return plain text, got %q", got)
	}
	bmp := prefix + strings.Repeat("中", 4000) // 12000+ UTF-8 bytes, 4020 UTF-16 units
	if got := Hyperlink("ok", bmp); !strings.Contains(got, "\x1b]8;;") {
		t.Fatalf("BMP URL under 8192 UTF-16 units should be linked")
	}
	astral := prefix + strings.Repeat("😀", 4100) // 8220 UTF-16 units
	if got := Hyperlink("ok", astral); got != "ok" {
		t.Fatalf("astral URL over 8192 UTF-16 units should return plain text")
	}
}
