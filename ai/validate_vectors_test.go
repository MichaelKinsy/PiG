package ai_test

// pi: packages/ai/src/utils/validation.ts (Pi b556cfe5)
//
// test/parity/sdk-vectors/pi-ai/validateToolArguments.json holds Pi's own answers to the cases of test/parity/sdk-vectors/cases/pi-ai/validateToolArguments.mjs: the validated arguments, or the message Pi threw.
// ValidateToolArguments must give the same arguments, in the same member order, and the same message.

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

func TestValidateToolArgumentsMatchesPiVectors(t *testing.T) {
	data, err := os.ReadFile("../test/parity/sdk-vectors/pi-ai/validateToolArguments.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors struct {
		Cases []struct {
			Name   string            `json:"name"`
			Args   []json.RawMessage `json:"args"`
			Result json.RawMessage   `json:"result"`
			Throws *string           `json:"throws"`
		} `json:"cases"`
	}
	if err := jsstring.UnmarshalKeepingSurrogates(data, &vectors); err != nil {
		t.Fatal(err)
	}
	if len(vectors.Cases) < 60 {
		t.Fatalf("%d vectors", len(vectors.Cases))
	}
	for _, vector := range vectors.Cases {
		t.Run(vector.Name, func(t *testing.T) {
			var tool ai.ToolSchema
			var call ai.ToolCall
			if err := json.Unmarshal(vector.Args[0], &tool); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(vector.Args[1], &call); err != nil {
				t.Fatal(err)
			}
			parameters, err := tool.ParametersJSON()
			if err != nil {
				t.Fatal(err)
			}
			arguments, err := call.ArgumentsJSON()
			if err != nil {
				t.Fatal(err)
			}
			got, err := ai.ValidateToolArgumentsJSON(tool.Name, parameters, arguments)
			if vector.Throws != nil {
				if err == nil || err.Error() != *vector.Throws {
					t.Fatalf("error = %v\nwant    %s", err, *vector.Throws)
				}
				return
			}
			want, canonicalErr := jsonstringify.Canonicalize(vector.Result)
			if canonicalErr != nil {
				t.Fatal(canonicalErr)
			}
			if err != nil || string(got) != string(want) {
				t.Fatalf("got %s, %v\nwant %s", got, err, want)
			}
		})
	}
}
