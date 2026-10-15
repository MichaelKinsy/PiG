package harness

import (
	"encoding/json"
	"testing"
)

// Pi's GenerationInput is Record<string, never> (generation.ts:49) and the harness creates the task with {}
// (generation.ts:675): the stored input is an empty object, and an empty object decodes back to an input with no members.
func TestGenerationInputIsPisEmptyRecord(t *testing.T) {
	got, err := json.Marshal(GenerationInput{})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "{}" {
		t.Fatalf("GenerationInput{} encodes as %s, Pi stores {}", got)
	}
	var back GenerationInput
	if err := json.Unmarshal([]byte("{}"), &back); err != nil {
		t.Fatal(err)
	}
	if back == nil || len(back) != 0 {
		t.Fatalf("{} decodes as %#v, want an empty record", back)
	}
}
