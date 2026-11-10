package mcpext

import (
	"strings"
	"testing"
)

// Pi 9e870f6f0: packages/coding-agent/src/extensions/mcp/index.ts `sliceHead(text, max - 1).trimEnd()` drops an astral
// character a cut would split, and runtime.ts `sliceTail` does the same for the stderr tail.
func TestTruncateDropsASplitSurrogatePair(t *testing.T) {
	text := strings.Repeat("a", 8) + "\U0001F600" + "zz"
	if got, want := truncateChars(text, 10), strings.Repeat("a", 8)+"…"; got != want {
		t.Fatalf("truncateChars = %q, want %q", got, want)
	}
}

func TestStderrTailDropsASplitSurrogatePair(t *testing.T) {
	stderr := "\U0001F600" + strings.Repeat("b", stderrTailChars-1)
	if got, want := stderrTailOf(stderr), strings.Repeat("b", stderrTailChars-1); got != want {
		t.Fatalf("stderrTailOf split a pair: %q", got)
	}
}
