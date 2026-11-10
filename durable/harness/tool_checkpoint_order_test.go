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

// A tool call's arguments are model output, and a Pi string in them can hold a lone UTF-16 surrogate: JSON.stringify writes the unit as
// the escape and JSON.parse reads it back. A Go string holds it as WTF-8, which encoding/json turned into U+FFFD on both sides.
func TestAToolCheckpointKeepsALoneSurrogateInItsArguments(t *testing.T) {
	const unit = "\xed\xa0\x80"
	const stored = `{"phase":"execute","arguments":{"a":"\ud800"},"replay":"safe"}`
	encoded, err := ToolTaskCheckpoint{Phase: toolPhaseExecute, Arguments: map[string]any{"a": unit}, Replay: durable.ReplaySafe}.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != stored {
		t.Errorf("encoded %s, want %s", encoded, stored)
	}
	var checkpoint ToolTaskCheckpoint
	if err := checkpoint.UnmarshalJSON([]byte(stored)); err != nil {
		t.Fatal(err)
	}
	if checkpoint.Arguments["a"] != unit {
		t.Errorf("decoded arguments %q, want the unit %q", checkpoint.Arguments["a"], unit)
	}
}

// The other texts the harness stores for a conversation or a tool call (extension and tool selections, a view's documents, a
// nested call's arguments) read and write a lone surrogate as JSON.parse and JSON.stringify do.
func TestHarnessJSONKeepsALoneSurrogateInSelectionsAndViewDocs(t *testing.T) {
	const unit = "\xed\xa0\x80"
	encoded, err := ExtensionSelection{Exact: true, Names: []string{unit}}.MarshalJSON()
	if err != nil || string(encoded) != `["\ud800"]` {
		t.Errorf("extension selection = %s, %v, want [\"\\ud800\"]", encoded, err)
	}
	var selection ExtensionSelection
	if err := selection.UnmarshalJSON([]byte(`{"add":["\ud800"]}`)); err != nil || selection.Add[0] != unit {
		t.Errorf("extension edit = %q, %v, want the unit kept", selection.Add, err)
	}
	var tools ToolSelection
	if err := tools.UnmarshalJSON([]byte(`["\ud800"]`)); err != nil || tools.Names[0] != unit {
		t.Errorf("tool selection = %q, %v, want the unit kept", tools.Names, err)
	}
	encoded, err = tools.MarshalJSON()
	if err != nil || string(encoded) != `["\ud800"]` {
		t.Errorf("tool selection = %s, %v, want [\"\\ud800\"]", encoded, err)
	}
	var docs ViewDocs
	if err := docs.UnmarshalJSON([]byte(`{"k\ud800":{"a":"\udc00"}}`)); err != nil {
		t.Fatal(err)
	}
	encoded, err = docs.MarshalJSON()
	if err != nil || string(encoded) != `{"k\ud800":{"a":"\udc00"}}` {
		t.Errorf("view documents = %s, %v, want the escapes kept", encoded, err)
	}
}
