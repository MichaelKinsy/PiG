package text

import (
	"bytes"
	"math/rand/v2"
	"strings"
	"testing"
)

// referenceStripANSI and referenceSanitizeBinaryOutput are the copying forms StripANSI and SanitizeBinaryOutput had before they scanned first.
func referenceStripANSI(b []byte) []byte {
	if len(b) == 0 {
		return b
	}
	if !strings.Contains(string(b), "\x1b") && !strings.Contains(string(b), "\u009b") {
		return b
	}
	return ansiRegex.Regexp().ReplaceAll(b, nil)
}

func referenceSanitizeBinaryOutput(s string) string {
	if s == "" {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			b.WriteRune(r)
		case r <= 0x1f:
		case r >= 0xfff9 && r <= 0xfffb:
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// sanitizePieces cover what command output carries: plain and multi-byte text, CSI and OSC sequences (whole and cut short at the end), the 8-bit CSI
// and string terminator, control and DEL bytes, U+FFF9..U+FFFB and the bytes around them, U+FFFD, and invalid or truncated UTF-8.
var sanitizePieces = []string{
	"plain text ", "\n", "\t", "\r\n", "\r", "é", "日本", "😀", "\x1b[31m", "\x1b[0m", "\x1b[38;5;", "\x1b[1;2", "\x1b]0;title\x07", "\x1b]8;;http://x\x1b\\",
	"\x1b]0;open", "\x1b", "\u009b31m", "\u009b", "\u009c", "\x00", "\x01", "\x07", "\x08", "\x1f", "\x7f", "\ufff9", "\ufffa", "\ufffb", "\ufff8", "\ufffc",
	"\xef\xbf", "\xef", "\ufffd", "\xff", "\x80", "\xc3", "\xe2\x82", "\xed\xa0\x80", "\xc0\xaf", "\xf4\x90\x80\x80",
}

func checkSanitizeEquivalence(t *testing.T, s string) {
	t.Helper()
	if got, want := SanitizeBinaryOutput(s), referenceSanitizeBinaryOutput(s); got != want {
		t.Fatalf("SanitizeBinaryOutput(%q) = %q, copying form %q", s, got, want)
	}
	if got, want := StripANSI([]byte(s)), referenceStripANSI([]byte(s)); !bytes.Equal(got, want) {
		t.Fatalf("StripANSI(%q) = %q, copying form %q", s, got, want)
	}
	if got, want := StripANSIString(s), string(referenceStripANSI([]byte(s))); got != want {
		t.Fatalf("StripANSIString(%q) = %q, StripANSI %q", s, got, want)
	}
	// The executor's chunk pipeline: strip, then sanitize.
	if got, want := SanitizeBinaryOutput(StripANSIString(s)), referenceSanitizeBinaryOutput(string(referenceStripANSI([]byte(s)))); got != want {
		t.Fatalf("sanitized stripped %q = %q, copying forms %q", s, got, want)
	}
}

// Scanning first changes no output: on any mix of the pieces the scan-first functions return exactly what the copying forms return.
func TestScanFirstStripAndSanitizeMatchTheCopyingForms(t *testing.T) {
	rng := rand.New(rand.NewPCG(13, 17))
	for range 20000 {
		var b strings.Builder
		for range rng.IntN(10) {
			b.WriteString(sanitizePieces[rng.IntN(len(sanitizePieces))])
		}
		checkSanitizeEquivalence(t, b.String())
	}
}

func FuzzScanFirstStripAndSanitize(f *testing.F) {
	for _, piece := range sanitizePieces {
		f.Add(piece + "tail")
	}
	f.Fuzz(checkSanitizeEquivalence)
}
