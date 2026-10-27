package engine

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

type vectorFile struct {
	Cases []struct {
		Name   string            `json:"name"`
		Args   []json.RawMessage `json:"args"`
		Result json.RawMessage   `json:"result"`
	} `json:"cases"`
}

type vectorResult struct {
	Wire     string   `json:"wire"`
	WireArgs []string `json:"wireArgs"`
}

// TestEngineMatchesTypeBox runs every vector of test/parity/sdk-vectors/pi-ai/Type.json whose outermost call is a
// member of the engine: the arguments and the result are the wire text of the values TypeBox itself built and
// returned, hidden members and key order included.
func TestEngineMatchesTypeBox(t *testing.T) {
	data, err := os.ReadFile("../../../test/parity/sdk-vectors/pi-ai/Type.json")
	if err != nil {
		t.Fatal(err)
	}
	var file vectorFile
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	ran := 0
	for _, c := range file.Cases {
		var node struct {
			Call string `json:"$call"`
		}
		if len(c.Args) != 1 || json.Unmarshal(c.Args[0], &node) != nil || !Has(node.Call) {
			continue
		}
		var result vectorResult
		if len(c.Result) == 0 || c.Result[0] != '{' || json.Unmarshal(c.Result, &result) != nil || result.WireArgs == nil {
			continue
		}
		t.Run(c.Name, func(t *testing.T) {
			args := make([]any, len(result.WireArgs))
			for i, w := range result.WireArgs {
				v, err := Decode(w)
				if err != nil {
					t.Fatalf("decode argument %d: %v", i, err)
				}
				args[i] = v
			}
			got, err := Call(node.Call, args)
			if err != nil {
				t.Fatalf("Call: %v", err)
			}
			if text := Encode(got); text != result.Wire {
				t.Fatalf("result differs\n got: %s\nwant: %s", text, result.Wire)
			}
		})
		ran++
	}
	if ran < 400 {
		t.Fatalf("only %d vectors ran", ran)
	}
	_ = strings.TrimSpace
}
