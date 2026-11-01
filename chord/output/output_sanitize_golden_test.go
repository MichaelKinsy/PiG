package output

// Ports packages/chord/src/output.ts (sanitizeOutput's ESCAPE_SEQUENCE, Pi 6d4b7443).

import (
	"encoding/json"
	"os"
	"os/exec"
	"testing"
)

// TestSanitizeOutputEscapeSequencesMatchPi checks SanitizeOutput against what Pi's sanitizeOutput (output.ts at
// 6d4b7443, run under Node) returned for the inputs of testdata/sanitize_escape_golden.mjs: the three upstream cases,
// truncated and unterminated sequences, and seeded random strings over ESC, the bracket and string-introducer bytes,
// BEL, `\`, controls and non-ASCII text. The file is a static record of Pi's answers, regenerated with
// `node testdata/sanitize_escape_golden.mjs <Pi tree at 6d4b7443> > testdata/sanitize_escape_golden.json` (the live
// oracle of output_pi_oracle_test.go needs the upstream mirror).
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
	generated := goldenGeneratorInputs(t)
	if len(cases) != len(generated) {
		t.Fatalf("%d golden cases, the generator makes %d inputs: regenerate the golden", len(cases), len(generated))
	}
	for i, c := range cases {
		if c.Input != generated[i] {
			t.Fatalf("golden input %d is %q, the generator makes %q: regenerate the golden", i, c.Input, generated[i])
		}
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

// goldenGeneratorInputs is the input list testdata/sanitize_escape_golden.mjs makes (`--inputs`: deterministic, no Pi
// tree needed), so a golden whose inputs drifted from the generator fails instead of passing on stale answers.
func goldenGeneratorInputs(t *testing.T) []string {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), "node", "testdata/sanitize_escape_golden.mjs", "--inputs").Output()
	if err != nil {
		t.Fatalf("golden generator: %v", err)
	}
	var inputs []string
	if err := json.Unmarshal(out, &inputs); err != nil {
		t.Fatal(err)
	}
	return inputs
}
