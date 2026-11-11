package harness

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// tool.ts:202-217: the rerun of a safe tool shows the arguments of the checkpoint it stored, in that member order. The
// checkpoint a task reads is decoded from its stored text, so the decode keeps the order and the spelling.
func TestAToolCheckpointKeepsItsArgumentsInTheirStoredOrderWhenDecoded(t *testing.T) {
	const arguments = `{"zeta":1.0,"alpha":{"y":"a","b":"c"},"text":"x"}`
	stored := `{"phase":"execute","arguments":` + arguments + `,"replay":"safe"}`
	var checkpoint ToolTaskCheckpoint
	if err := json.Unmarshal([]byte(stored), &checkpoint); err != nil {
		t.Fatal(err)
	}
	if checkpoint.Phase != toolPhaseExecute || checkpoint.Replay != durable.ReplaySafe || checkpoint.Arguments["text"] != "x" {
		t.Fatalf("decoded %+v, want the execute phase, safe replay and the arguments by value", checkpoint)
	}
	shown, err := checkpoint.argumentsText()
	if err != nil {
		t.Fatal(err)
	}
	if string(shown) != arguments {
		t.Errorf("the rerun shows %s, want the stored order %s", shown, arguments)
	}
	encoded, err := json.Marshal(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"phase":"execute","arguments":` + arguments + `,"replay":"safe"}`; string(encoded) != want {
		t.Errorf("re-encoded %s, want %s", encoded, want)
	}
}

func TestAToolCheckpointWithoutArgumentsShowsAnEmptyObject(t *testing.T) {
	for _, stored := range []string{`{"phase":"execute","replay":"safe"}`, `{"phase":"execute","arguments":null,"replay":"safe"}`} {
		var checkpoint ToolTaskCheckpoint
		if err := json.Unmarshal([]byte(stored), &checkpoint); err != nil {
			t.Fatal(err)
		}
		shown, err := checkpoint.argumentsText()
		if err != nil {
			t.Fatal(err)
		}
		if string(shown) != "{}" {
			t.Errorf("%s: the rerun shows %s, want {}", stored, shown)
		}
	}
}
