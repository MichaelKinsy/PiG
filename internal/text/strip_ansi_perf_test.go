package text

import (
	"strings"
	"testing"
)

// Clean text, the common case for command output, passes through StripANSI and SanitizeBinaryOutput without a copy: both scan first and build a new value
// only when something must be removed.
func TestCleanTextIsNotCopied(t *testing.T) {
	clean := strings.Repeat("plain command output, tab\tand CR\r\n", 120) + "日本語 é 😀"
	cleanBytes := []byte(clean)
	for name, run := range map[string]func(){
		"SanitizeBinaryOutput": func() { _ = SanitizeBinaryOutput(clean) },
		"StripANSI":            func() { _ = StripANSI(cleanBytes) },
	} {
		if allocs := testing.AllocsPerRun(50, run); allocs != 0 {
			t.Errorf("%s of clean text: %v allocations, want 0", name, allocs)
		}
	}
}
