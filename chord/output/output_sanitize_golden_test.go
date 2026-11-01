package output

// Ports packages/chord/src/output.ts (sanitizeOutput's ESCAPE_SEQUENCE, Pi 6d4b7443).

import (
	"encoding/json"
	"os"
	"testing"
)

// TestSanitizeOutputEscapeSequencesMatchPi checks SanitizeOutput against what Pi's sanitizeOutput (output.ts at
// 6d4b7443, run under Node) returned for 1512 inputs: the three upstream cases, truncated and unterminated sequences,
// and random strings over ESC, the bracket and string-introducer bytes, BEL, `\`, parameter, intermediate and final
// bytes, controls and non-ASCII text. The file is a static record of Pi's answers (the live oracle of
// output_pi_oracle_test.go needs the upstream mirror).
func TestSanitizeOutputEscapeSequencesMatchPi(t *testing.T) {
	raw, err := os.ReadFile("testdata/sanitize_escape_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Input  string `json:"input"`
		Output string `json:"output"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 1512 {
		t.Fatalf("%d golden cases, want 1512", len(cases))
	}
	differing := 0
	for _, c := range cases {
		if got := SanitizeOutput(c.Input); got != c.Output {
			differing++
			if differing <= 6 {
				t.Errorf("SanitizeOutput(%q) = %q, Pi returned %q", c.Input, got, c.Output)
			}
		}
	}
	if differing > 0 {
		t.Errorf("%d of %d inputs differ from Pi", differing, len(cases))
	}
}
