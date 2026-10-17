//go:build !pig_strip_word_dictionaries

package wordsegmenter

import (
	"encoding/json"
	"os"
	"testing"
)

// The five embedded dictionaries are borrowed in place on the input loop (README: no decompression there), so their bytes are binary size every Pig carries. The reviewed budget is the size measured when it was set; a dictionary that grows needs new binary-size evidence, and a smaller index lowers the budget.
func TestEmbeddedDictionariesStayWithinTheReviewedSize(t *testing.T) {
	data, err := os.ReadFile("../../automation/perf/startup-budgets.json")
	if err != nil {
		t.Fatal(err)
	}
	var budget struct {
		WordDictionariesMaxBytes int `json:"word_dictionaries_max_bytes"`
	}
	if err := json.Unmarshal(data, &budget); err != nil {
		t.Fatal(err)
	}
	if budget.WordDictionariesMaxBytes <= 0 {
		t.Fatal("missing word dictionary budget")
	}
	total := len(cjkDictionary) + len(thaiDictionary) + len(laoDictionary) + len(khmerDictionary) + len(burmeseDictionary)
	if total > budget.WordDictionariesMaxBytes {
		t.Fatalf("embedded word dictionaries are %d bytes, over the reviewed budget %d", total, budget.WordDictionariesMaxBytes)
	}
}
