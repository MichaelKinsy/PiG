package extension

import (
	"encoding/json"
	"testing"
)

// types.ts:1472 SessionBeforeCompactResult.compaction is a CompactionResult whose `details` is the extension's own object: a JS object keeps the member order it was written in, so decoding and encoding the result keeps it (the session file stores the details as written).
func TestSessionBeforeCompactResultKeepsTheDetailsMemberOrder(t *testing.T) {
	const wire = `{"cancel":true,"compaction":{"summary":"s","firstKeptEntryId":"e1","tokensBefore":7,"details":{"zeta":1,"alpha":{"b":1,"a":2}}}}`
	var result SessionBeforeCompactResult
	if err := json.Unmarshal([]byte(wire), &result); err != nil {
		t.Fatal(err)
	}
	if result.Compaction == nil || result.Compaction.Summary != "s" || result.Compaction.FirstKeptEntryID != "e1" || result.Compaction.TokensBefore != 7 || !result.Cancel {
		t.Fatalf("decoded = %+v", result)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Compaction struct {
			Details json.RawMessage `json:"details"`
		} `json:"compaction"`
	}
	if err := json.Unmarshal(encoded, &out); err != nil {
		t.Fatal(err)
	}
	if string(out.Compaction.Details) != `{"zeta":1,"alpha":{"b":1,"a":2}}` {
		t.Fatalf("details = %s", out.Compaction.Details)
	}
}
