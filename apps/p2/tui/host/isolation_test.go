package host_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/tui/host"
)

func filter(t *testing.T, input string) string {
	t.Helper()
	out, err := host.FilterTerminalOutput(input, host.BaseModes)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestTerminalOutputAdmitsOnlyBoundedCompleteAllowedSequences(t *testing.T) {
	safe := "text\r\n\t界\x1b[38;2;1;2;3mred\x1b[0m\x1b[2J\x1b[H\x1b[3A\x1b[?25l\x1b[?2026h\x1b[?2004h\x1b[?1049h\x1b[1;24r"
	if got := filter(t, safe); got != safe {
		t.Fatalf("safe sequence changed: %q", got)
	}
	for _, sequence := range []string{
		"\x1b]52;c;secret\x07", "\x1b]8;;https://evil\x1b\\", "\x1b]0;title\x07", "\x1bPpayload\x1b\\", "\x1b_Gpayload\x1b\\", "\x1b^payload\x1b\\",
		"\x1bXpayload\x1b\\", "\x1b[?1000h", "\x1b[?1006h", "\x1b[>7u", "\x1b[5n", "\x1b[999999999A", "\x1b(0", "\u009b2J", "\u009d52;c;secret\u009c", "\x00\x07\x7f",
	} {
		if got := filter(t, "a"+sequence+"b"); got != "ab" {
			t.Fatalf("%q: %q", sequence, got)
		}
	}
	for _, partial := range []string{"\x1b", "\x1b[", "\x1b[12;", "\x1b]52;secret", "\x1bPdata"} {
		if got := filter(t, "ok"+partial); got != "ok" {
			t.Fatalf("%q: %q", partial, got)
		}
	}
	for _, big := range []string{strings.Repeat("x", host.MaxTerminalFrame+1), strings.Repeat("界", host.MaxTerminalFrame)} {
		if _, err := host.FilterTerminalOutput(big, host.BaseModes); err == nil || !strings.Contains(err.Error(), "64 KiB") {
			t.Fatalf("oversized frame: %v", err)
		}
	}
	// Deterministic fuzz: filtered output must be idempotent and every surviving ESC must be an allowed complete CSI.
	hostile := regexp.MustCompile("[\u0000-\u0008\u000b\u000c\u000e-\u001a\u001c-\u001f\u007f-\u009f]")
	csi := regexp.MustCompile(`\x1b\[[\d;:?]*[A-Za-z]`)
	seed := uint32(123456)
	for round := 0; round < 4000; round++ {
		var input strings.Builder
		for range 96 {
			seed = seed*1664525 + 1013904223
			input.WriteRune(rune(seed % 256))
		}
		result := filter(t, input.String())
		if again := filter(t, result); again != result {
			t.Fatalf("not idempotent: %q -> %q", result, again)
		}
		if hostile.MatchString(result) || strings.Contains(csi.ReplaceAllString(result, ""), "\x1b") {
			t.Fatalf("hostile output survived: %q", result)
		}
	}
}
