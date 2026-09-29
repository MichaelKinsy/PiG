package tui

import (
	"encoding/json"
	"fmt"
	"testing"
)

// Numeric expectations are from the published Pi 0.87.1 fuzzyMatch, which indexes UTF-16 units and tests JavaScript whitespace boundaries.
func TestFuzzyMatchUnicodeScores(t *testing.T) {
	for _, tc := range []struct {
		query, text string
		score       float64
	}{
		{"é", "café", 0.30000000000000004},
		{"😀", "x😀", -4.7},
		{"ab", "a\ufeffb", -22.8},
		{"ab", "a\u0085b", -12.8},
		{"i\u0307", "İ", -124.9},
		{"ος", "ΟΣ", -124.9},
	} {
		t.Run(tc.text, func(t *testing.T) {
			got := FuzzyMatchScore(tc.query, tc.text)
			if !got.Matches || got.Score != tc.score {
				t.Fatalf("FuzzyMatchScore(%q, %q) = %+v, want score %v", tc.query, tc.text, got, tc.score)
			}
			record, err := json.Marshal([]any{tc.query, tc.text, got.Score})
			if err != nil {
				t.Fatal(err)
			}
			fmt.Printf("FUZZY_UNICODE %s\n", record)
		})
	}
}
