package agent

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

type validationOracleTool struct {
	fakeTool
	schema json.RawMessage
}

func (t *validationOracleTool) ArgumentSchema() json.RawMessage { return t.schema }

// TestToolArgumentValidationPiOracle compares the production preparation path with
// Pi 0.87.1 validateToolArguments, including the unmodified received-arguments error.
func TestToolArgumentValidationPiOracle(t *testing.T) {
	data, err := os.ReadFile("testdata/tool-validation.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Schemas []json.RawMessage `json:"schemas"`
		Cases   []struct {
			Name   string          `json:"name"`
			Tool   string          `json:"tool"`
			Schema int             `json:"schema"`
			Input  json.RawMessage `json:"input"`
			Output json.RawMessage `json:"output"`
			Error  string          `json:"error"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, row := range fixture.Cases {
		t.Run(row.Name, func(t *testing.T) {
			var params map[string]any
			if err := json.Unmarshal(fixture.Schemas[row.Schema], &params); err != nil {
				t.Fatal(err)
			}
			tool := &validationOracleTool{fakeTool: fakeTool{name: row.Tool, params: params}, schema: fixture.Schemas[row.Schema]}
			a := NewAgent(AgentOptions{Tools: []AgentTool{tool}})
			call := pendingToolCall{id: "call", name: row.Tool, args: toolCallArguments(row.Input)}
			got := a.prepareToolCall(context.Background(), call)
			if row.Error != "" {
				if got.finalized == nil || got.finalized.result.Text() != row.Error {
					t.Fatalf("error = %+v; want %s", got.finalized, row.Error)
				}
				return
			}
			if got.prepared == nil {
				t.Fatalf("rejected valid args: %+v", got.finalized)
			}
			var actual, expected any
			if err := json.Unmarshal(got.prepared.args, &actual); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(row.Output, &expected); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(actual, expected) {
				t.Fatalf("args = %s; want %s", got.prepared.args, row.Output)
			}
		})
	}
}
