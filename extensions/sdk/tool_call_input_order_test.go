package sdk

import (
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// A tool_call handler's edit reaches the tool in the member order a JavaScript object would hold: a member the host sent keeps its place, a member the handler added follows, a removed member is gone, at every depth.
func TestToolCallInputEditKeepsTheHostsMemberOrder(t *testing.T) {
	source := json.RawMessage(`{"timeout":5,"drop":1,"command":"ls","nested":{"z":1,"a":2},"list":[{"y":1,"b":2}]}`)
	var input map[string]any
	if err := json.Unmarshal(source, &input); err != nil {
		t.Fatal(err)
	}
	call := newToolCallInput(input, source)
	if edited, ok := call.edit(); ok {
		t.Fatalf("an untouched input counted as an edit: %s", edited.(json.RawMessage))
	}
	input["command"] = "pwd"
	delete(input, "drop")
	input["zeta"] = true
	input["alpha"] = 1.0
	input["nested"].(map[string]any)["m"] = 3.0
	input["list"].([]any)[0].(map[string]any)["c"] = 4.0
	edited, ok := call.edit()
	if !ok {
		t.Fatal("an edited input did not count as an edit")
	}
	want := `{"timeout":5,"command":"pwd","nested":{"z":1,"a":2,"m":3},"list":[{"y":1,"b":2,"c":4}],"alpha":1,"zeta":true}`
	if got := string(edited.(json.RawMessage)); got != want {
		t.Errorf("edited input =\n%s\nwant\n%s", got, want)
	}
}

// Without the host's text there is no order to keep, and members are sorted.
func TestToolCallInputEditWithoutSourceIsSorted(t *testing.T) {
	input := map[string]any{"b": 1.0}
	call := newToolCallInput(input, nil)
	input["a"] = 2.0
	edited, ok := call.edit()
	if !ok || string(edited.(json.RawMessage)) != `{"a":2,"b":1}` {
		t.Fatalf("edit = %v, %v", edited, ok)
	}
}
