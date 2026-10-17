package coding

import (
	"encoding/json"
	"strings"
	"testing"
)

// Pi packages/coding-agent/src/core/extensions/types.ts:818 TreePreparation.entriesToSummarize is SessionEntry[]: a navigation that
// abandons no entries hands session_before_tree an empty array, which a subprocess extension receives as [] and never as null.
func TestTreePreparationEmptyEntriesToSummarizeIsAnArray(t *testing.T) {
	encoded, err := json.Marshal(TreePreparation{TargetID: "target", EntriesToSummarize: sessionEntryValues(nil)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"entriesToSummarize":[]`) {
		t.Fatalf("preparation = %s, want entriesToSummarize []", encoded)
	}
}
