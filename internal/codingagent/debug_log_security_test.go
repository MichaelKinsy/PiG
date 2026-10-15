package codingagent

import (
	"strings"
	"testing"
)

// PIG_DEBUG_KEYS logs the key a diagnosis needs, never the text typed or pasted into a masked login prompt.
func TestRedactKeyChunkHidesTypedAndPastedText(t *testing.T) {
	for chunk, want := range map[string]string{
		"sk-secret-key":               "text(13 chars)",
		"é":                           "text(1 chars)",
		"\x1b[200~sk-secret\x1b[201~": "paste(21 bytes)",
		"\x1b[A":                      `"\x1b[A"`,
		"\x03":                        `"\x03"`,
		"\r":                          `"\r"`,
		"\x7f":                        `"\x7f"`,
	} {
		got := redactKeyChunk(chunk)
		if got != want {
			t.Errorf("redactKeyChunk(%q) = %s, want %s", chunk, got, want)
		}
		if strings.Contains(got, "secret") {
			t.Errorf("redactKeyChunk(%q) leaks the text: %s", chunk, got)
		}
	}
}
