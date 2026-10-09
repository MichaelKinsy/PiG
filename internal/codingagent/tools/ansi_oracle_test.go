package tools

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// pi: packages/coding-agent/src/utils/ansi.ts

// stripAnsi and splitIncompleteAnsiSuffix against pinned Pi over seeded text built from complete and truncated CSI, OSC (BEL and ST
// terminated), 8-bit CSI, charset and private sequences, plain text with multi-byte characters, and long unfinished tails.
func TestAnsiMatchesPi(t *testing.T) {
	state := uint64(0x9e3779b97f4a7c15)
	next := func(n int) int {
		state ^= state << 13
		state ^= state >> 7
		state ^= state << 17
		return int(state % uint64(n))
	}
	pieces := []string{
		"plain", " ", "é", "日本", "🙂", "\n", "\t", "\x1b[31m", "\x1b[0m", "\x1b[1;38;5;196m", "\x1b[2K", "\x1b[?25h", "\x1b[", "\x1b[38;2;1;2;3",
		"\x1b]0;title\x07", "\x1b]8;;http://x\x1b\\", "\x1b]8;;", "\x1b]0;unfinished", "\x1b", "\x1b(B", "\x1b=", "\x1b7", "\u009b31m", "\u009b", "\u009b1;",
		"\x1b[31", "\x1bP", "\x1b\\", "\x07", "\x1b[999999m", "[31m", "\x1b[1;2;3;4;5;6;7;8m",
	}
	var inputs []string
	for range 1500 {
		var b strings.Builder
		for range next(12) {
			b.WriteString(pieces[next(len(pieces))])
		}
		inputs = append(inputs, b.String())
	}
	// A text longer than the 256-unit window with an unfinished sequence at its end, and one whose unfinished sequence is itself longer.
	inputs = append(inputs, strings.Repeat("a", 400)+"\x1b[31", strings.Repeat("a", 100)+"\x1b]0;"+strings.Repeat("t", 300), "\x1b]0;"+strings.Repeat("t", 255),
		"\x1b]0;"+strings.Repeat("t", 256), strings.Repeat("é", 200)+"\x1b[", "")
	// Unfinished sequences whose length straddles the 256-code-unit window, in BMP and astral (two-unit) text.
	for n := 248; n <= 264; n++ {
		inputs = append(inputs,
			strings.Repeat("a", 50)+"\x1b["+strings.Repeat("1", n-2),
			strings.Repeat("a", 50)+"\x1b]0;"+strings.Repeat("t", n-4),
			strings.Repeat("a", 50)+"\x1b]0;"+strings.Repeat("🙂", (n-4)/2)+strings.Repeat("t", (n-4)%2),
			strings.Repeat("🙂", 40)+"\x1b["+strings.Repeat("1", n-2),
			strings.Repeat("a", n-1)+"\x1b[",
			strings.Repeat("a", n)+"\x1b[",
		)
	}
	input, err := json.Marshal(inputs)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/ansi.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []struct {
		Strip string `json:"strip"`
		Split struct {
			Complete string `json:"complete"`
			Pending  string `json:"pending"`
		} `json:"split"`
	}
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	failures := 0
	for i, in := range inputs {
		want := expected[i]
		if got := string(StripANSI([]byte(in))); got != want.Strip {
			if failures++; failures <= 6 {
				t.Errorf("StripANSI(%q) = %q, Pi %q", in, got, want.Strip)
			}
		}
		if complete, pending := SplitIncompleteAnsiSuffix(in); complete != want.Split.Complete || pending != want.Split.Pending {
			if failures++; failures <= 6 {
				t.Errorf("SplitIncompleteAnsiSuffix(%q) = %q|%q, Pi %q|%q", in, complete, pending, want.Split.Complete, want.Split.Pending)
			}
		}
	}
	if failures > 6 {
		t.Errorf("%d differences from Pi over %d inputs", failures, len(inputs))
	}
}
