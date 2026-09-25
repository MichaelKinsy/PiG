package tools

import (
	"encoding/json"
	"os"
	"testing"
)

// The unified patch must be byte-identical to upstream jsdiff
// createTwoFilesPatch(..., {context:4, headerOptions: FILE_HEADERS_ONLY}).
// The oracle corpus is generated directly from pi 0.78.1's bundled `diff`
// package (see testdata/unified_patch_oracle.json) over edge cases and 60
// seeded fuzz pairs.
func TestGenerateUnifiedPatch_MatchesJsdiffOracle(t *testing.T) {
	raw, err := os.ReadFile("testdata/unified_patch_oracle.json")
	if err != nil {
		t.Fatalf("read oracle: %v", err)
	}
	var cases []struct {
		Path string `json:"path"`
		Old  string `json:"old"`
		New  string `json:"new"`
		Want string `json:"want"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatalf("parse oracle: %v", err)
	}
	if len(cases) < 50 {
		t.Fatalf("oracle corpus too small (%d); regenerate", len(cases))
	}
	for i, c := range cases {
		got := GenerateUnifiedPatch(c.Path, c.Old, c.New)
		if got != c.Want {
			t.Errorf("case %d (%s) mismatch:\n--- got ---\n%q\n--- want ---\n%q\n--- old ---\n%q\n--- new ---\n%q",
				i, c.Path, got, c.Want, c.Old, c.New)
		}
	}
}

// A no-op edit yields only the two file-header lines (no hunks), matching
// jsdiff for identical inputs.
func TestGenerateUnifiedPatch_Identical(t *testing.T) {
	if got := GenerateUnifiedPatch("x", "same\n", "same\n"); got != "--- x\n+++ x\n" {
		t.Errorf("identical patch = %q, want %q", got, "--- x\n+++ x\n")
	}
}
