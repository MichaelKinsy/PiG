package tools

import (
	"regexp"
	"strings"
	"testing"
)

// Ports the generated-input half of packages/coding-agent/test/ansi-utils.test.ts:30-76: stripAnsi must equal the reference
// chalk/strip-ansi regex (packages/coding-agent/src/utils/ansi.ts:30-40) on every generated input.
func TestStripANSIMatchesTheReferencePatternOnGeneratedInputs(t *testing.T) {
	const st = `(?:\x07|\x1b\\|\x{9c})`
	reference := regexp.MustCompile(`(?:\x1b\][\s\S]*?` + st + `)|[\x1b\x{9b}][\[\]()#;?]*(?:\d{1,4}(?:[;:]\d{0,4})*)?[\dA-PR-TZcf-nq-uy=><~]`)
	referenceStrip := func(value string) string {
		if !strings.Contains(value, "\x1b") && !strings.Contains(value, "\u009b") {
			return value
		}
		return reference.ReplaceAllString(value, "")
	}
	inputs := []string{
		"plain", "a\x1b[31mred\x1b[0mz", "a\x1b]8;;https://example.com\x07link\x1b]8;;\x07z", "a\x1b]unterminated",
		"a\x1b]funterminated", "a\x1bPabc\x1b\\z", "a\x1b^abc\x07z", "a\x1b_abc\u009cz", "a\u0090abc\u009cz", "a\u009dabc\u009cz",
		"a\u009b31mred", "a\x1b(0x", "a\x1b*0x", "a\x1b+c", "a\x1b/0x", "a\x1bcok", "a\x1b\\ok",
	}
	chars := []string{"a", "f", "0", "1", ";", ":", "[", "]", "(", ")", "#", "?", "m", "P", "_", `\`, "\x07", "\x1b", "\u009b", "\u009c", "\u0090", "\u009d"}
	for _, char := range chars {
		inputs = append(inputs, "x\x1b"+char+"y", "x\u009b"+char+"y")
		for index := 0; index < len(chars); index += 3 {
			inputs = append(inputs, "x\x1b"+char+chars[index]+"y")
		}
	}
	if len(inputs) < 100 {
		t.Fatalf("only %d generated inputs", len(inputs))
	}
	for _, input := range inputs {
		if got, want := string(StripANSI([]byte(input))), referenceStrip(input); got != want {
			t.Errorf("StripANSI(%q) = %q, want %q", input, got, want)
		}
	}
}

// ansi.ts:24 the final byte of a CSI-family sequence is one of [0-9A-PR-TZcf-nq-uy=><~]: ESC followed by any other character is kept.
func TestStripANSIFinalByteSet(t *testing.T) {
	for code := rune(0x20); code < 0x7f; code++ {
		char := string(code)
		stripped := strings.ContainsRune("0123456789ABCDEFGHIJKLMNOPRSTZcfghijklmnqrstuy=><~", code)
		want := "\x1b" + char + "ok"
		if stripped {
			want = "ok"
		}
		if got := string(StripANSI([]byte("\x1b" + char + "ok"))); got != want {
			t.Errorf("ESC %q: got %q, want %q", char, got, want)
		}
	}
	// Parameters accept ";" and ":" separators (ansi.ts:24), up to four digits per group.
	if got := string(StripANSI([]byte("a\x1b[38:2:1:2:3mb\x1b[1;2;3Hc"))); got != "abc" {
		t.Errorf("separators: %q", got)
	}
	// The 8-bit string terminator ends an OSC and the 8-bit CSI introduces a sequence on its own.
	if got := string(StripANSI([]byte("a\x1b]0;title\u009cb"))); got != "ab" {
		t.Errorf("OSC ended by C1 ST: %q", got)
	}
}
