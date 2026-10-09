package compactiontypes

import (
	"encoding/json"
	"testing"
)

// compaction/utils.ts FileOperations holds three Sets; an extension receiving session_before_compact sees each as an array in JavaScript sort order (UTF-16 code units), so a supplementary-plane path sorts before U+FFFD.
func TestFileOperationsMarshalSetsInJavaScriptOrder(t *testing.T) {
	ops := FileOperations{
		Read:    map[string]struct{}{"b": {}, "a": {}, "\U0001F600": {}, "\uFFFD": {}},
		Written: map[string]struct{}{},
	}
	raw, err := json.Marshal(CompactionPreparation{FileOps: ops})
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		FileOps struct {
			Read    []string `json:"read"`
			Written []string `json:"written"`
			Edited  []string `json:"edited"`
		} `json:"fileOps"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	want := []string{"a", "b", "\U0001F600", "\uFFFD"}
	if len(wire.FileOps.Read) != 4 || wire.FileOps.Read[0] != want[0] || wire.FileOps.Read[1] != want[1] || wire.FileOps.Read[2] != want[2] || wire.FileOps.Read[3] != want[3] {
		t.Fatalf("read = %q, want %q", wire.FileOps.Read, want)
	}
	if wire.FileOps.Written == nil || wire.FileOps.Edited == nil {
		t.Fatalf("empty Sets must marshal as [], got %s", raw)
	}
}
