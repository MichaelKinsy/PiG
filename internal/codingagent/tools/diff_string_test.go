package tools

import (
	"encoding/json"
	"os"
	"testing"
)

// GenerateDiffString must be byte-identical to upstream generateDiffString
// (edit-diff.ts), including line-number padding, context elision (`...`),
// and the first-changed-line number. The oracle corpus is generated from
// pi 0.78.1's compiled edit-diff.js over edge cases and seeded fuzz pairs.
func TestGenerateDiffString_MatchesUpstreamOracle(t *testing.T) {
	raw, err := os.ReadFile("testdata/diff_string_oracle.json")
	if err != nil {
		t.Fatalf("read oracle: %v", err)
	}
	var cases []struct {
		Name             string `json:"name"`
		Old              string `json:"old"`
		New              string `json:"new"`
		Diff             string `json:"diff"`
		FirstChangedLine int    `json:"firstChangedLine"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatalf("parse oracle: %v", err)
	}
	if len(cases) < 50 {
		t.Fatalf("oracle corpus too small (%d); regenerate", len(cases))
	}
	for _, c := range cases {
		result := GenerateDiffString(c.Old, c.New)
		diff, first := result.Diff, firstLine(result)
		if diff != c.Diff {
			t.Errorf("case %q diff mismatch:\n--- got ---\n%q\n--- want ---\n%q\n--- old ---\n%q\n--- new ---\n%q",
				c.Name, diff, c.Diff, c.Old, c.New)
		}
		if first != c.FirstChangedLine {
			t.Errorf("case %q firstChangedLine = %d, want %d", c.Name, first, c.FirstChangedLine)
		}
	}
}

// An identical edit yields an empty diff and firstChangedLine 0 (upstream
// `undefined`).
func TestGenerateDiffString_Identical(t *testing.T) {
	result := GenerateDiffString("same\n", "same\n")
	if result.Diff != "" || result.FirstChangedLine != nil {
		t.Errorf("identical = (%q, %v), want (\"\", nil)", result.Diff, result.FirstChangedLine)
	}
}

// firstLine reads FirstChangedLine with upstream `undefined` as 0.
func firstLine(r DiffStringResult) int {
	if r.FirstChangedLine == nil {
		return 0
	}
	return *r.FirstChangedLine
}

// A change reports its first changed line as a present value, not nil.
func TestGenerateDiffString_FirstChangedLineIsPresentOnChange(t *testing.T) {
	result := GenerateDiffString("a\nb\n", "a\nB\n")
	if result.FirstChangedLine == nil || *result.FirstChangedLine != 2 {
		t.Fatalf("FirstChangedLine = %v, want 2", result.FirstChangedLine)
	}
}
