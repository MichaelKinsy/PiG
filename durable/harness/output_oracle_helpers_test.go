package harness

import (
	"math/rand"
	"strings"
)

// Helpers of the Pi oracle tests that drive tool output through the harness; the output buffer itself is tested in chord/output.

type outputLimitsJSON struct {
	MaxBytes int    `json:"maxBytes"`
	MaxLines int    `json:"maxLines"`
	Retain   string `json:"retain"`
}

var outputPieces = []string{"a", "bc", "\n", "line\n", "two\nlines\n", "é", "😀", "\ufeff", "\x01x", "\ufff9y\ufffb", "\r\n", "日本語", "\t", "\x7f", "\u2028", " ", "\x00", "\x1f", "tail without newline"}

func outputText(random *rand.Rand) string {
	var builder strings.Builder
	for range random.Intn(6) {
		if random.Intn(25) == 0 {
			builder.WriteString(strings.Repeat("0123456789", 1+random.Intn(8)))
			continue
		}
		builder.WriteString(outputPieces[random.Intn(len(outputPieces))])
	}
	return builder.String()
}

func ptrOutputText(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}
