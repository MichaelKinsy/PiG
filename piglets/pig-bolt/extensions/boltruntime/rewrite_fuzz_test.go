package boltruntime

import (
	"encoding/json"
	"os"
	"testing"
)

// TestRewriteFuzzCorpus runs rewriteModule over the corpus bench/bolt-aot/rewrite-exts/fuzz/gen.mjs wrote ($PIG_AOT_FUZZ_CORPUS) and writes the outputs, in order, to
// $PIG_AOT_FUZZ_OUT as a JSON array of strings; compare.mjs sets them against the expected rewrites and Pi-Bolt's rewriteCopy. $PIG_AOT_FUZZ_HAND_OUT gets the hand cases of
// this package's tests (file, source, expected) in the same format as the corpus. Both skip without their variable.
func TestRewriteFuzzCorpus(t *testing.T) {
	path := os.Getenv("PIG_AOT_FUZZ_CORPUS")
	if path == "" {
		t.Skip("set PIG_AOT_FUZZ_CORPUS and PIG_AOT_FUZZ_OUT to run the differential fuzz (bench/bolt-aot/rewrite-exts/fuzz/run.sh)")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var items []struct{ File, Src string }
	if err := json.Unmarshal(data, &items); err != nil {
		t.Fatal(err)
	}
	outputs := make([]string, len(items))
	for i, item := range items {
		outputs[i] = rewriteModule(item.Src, item.File).Source
	}
	encoded, err := json.Marshal(outputs)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(os.Getenv("PIG_AOT_FUZZ_OUT"), encoded, 0o644); err != nil {
		t.Fatal(err)
	}
}
