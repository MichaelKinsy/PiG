package rpcclient

import (
	"bytes"
	"encoding/json"
	"math"
	"os/exec"
	"strconv"
	"testing"
)

// serializeJsonLine is `${JSON.stringify(value)}\n` (jsonl.ts:11): numbers print in ECMAScript Number-to-string form (1e21 is "1e+21", -0 is
// "0") and non-finite numbers are null, in objects and arrays alike.
func TestSerializeJsonLineNumbersMatchNode(t *testing.T) {
	tokens := []string{"0", "-0", "1", "-1", "0.1", "0.30000000000000004", "1e21", "1e20", "123456789012345680000", "1e-6", "1e-7", "1.5e-7", "5e-324", "1.7976931348623157e308",
		"9007199254740993", "4294967296", "100", "1e2", "0.000001", "0.0000001", "123.456e5", "NaN", "Infinity", "-Infinity", "2.2250738585072014e-308", "1.2345678901234567e+25"}
	input, _ := json.Marshal(tokens)
	cmd := exec.CommandContext(t.Context(), "node", "testdata/json_numbers.mjs")
	cmd.Stdin = bytes.NewReader(input)
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Node: %v", err)
	}
	var want []string
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	for i, token := range tokens {
		var value float64
		switch token {
		case "NaN":
			value = math.NaN()
		case "Infinity":
			value = math.Inf(1)
		case "-Infinity":
			value = math.Inf(-1)
		case "-0":
			value = math.Copysign(0, -1)
		default:
			var err error
			if value, err = strconv.ParseFloat(token, 64); err != nil {
				t.Fatal(err)
			}
		}
		// The string member is String(value) in Node; compute it with the same ECMAScript rule through the encoder's own output for finite values.
		frame, err := SerializeJsonLine(struct {
			V float64   `json:"v"`
			A []float64 `json:"a"`
		}{value, []float64{value}})
		if err != nil {
			t.Errorf("%s: SerializeJsonLine failed: %v (Node frames %q)", token, err, want[i])
			continue
		}
		var node struct {
			V json.RawMessage   `json:"v"`
			A []json.RawMessage `json:"a"`
		}
		if err := json.Unmarshal([]byte(want[i]), &node); err != nil {
			t.Fatal(err)
		}
		wantFrame := `{"v":` + string(node.V) + `,"a":[` + string(node.A[0]) + "]}\n"
		if string(frame) != wantFrame {
			t.Errorf("%s: Pig %q, Node %q", token, frame, wantFrame)
		}
	}
}
