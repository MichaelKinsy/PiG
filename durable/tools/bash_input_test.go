package tools

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// packages/durable/src/tools/bash.ts:6-12,20 bashSchema and BashToolInput = Static<typeof bashSchema>: `command` is the required
// string, `timeout` the optional number of seconds. The arguments of a bash call decode into it as the schema validates them.
func TestBashToolInputIsTheBashSchemaArguments(t *testing.T) {
	var schema struct {
		Required   []string                   `json:"required"`
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal([]byte(bashParameters), &schema); err != nil {
		t.Fatal(err)
	}
	if len(schema.Required) != 1 || schema.Required[0] != "command" || len(schema.Properties) != 2 {
		t.Fatalf("schema = %+v, want command required and command+timeout properties", schema)
	}

	input, err := durable.FromJsonValue[BashToolInput](map[string]any{"command": "echo hi", "timeout": 1.5})
	if err != nil || input.Command != "echo hi" || input.Timeout == nil || *input.Timeout != 1.5 {
		t.Fatalf("input = %+v, %v", input, err)
	}
	input, err = durable.FromJsonValue[BashToolInput](map[string]any{"command": "ls"})
	if err != nil || input.Command != "ls" || input.Timeout != nil {
		t.Fatalf("a call without timeout has no timeout: %+v, %v", input, err)
	}
	if _, err := durable.FromJsonValue[BashToolInput](map[string]any{"command": "ls", "timeout": "soon"}); err == nil {
		t.Fatal("a timeout that is not a number must not decode")
	}
	encoded, err := json.Marshal(BashToolInput{Command: "ls"})
	if err != nil || string(encoded) != `{"command":"ls"}` {
		t.Fatalf("encoded = %s, %v", encoded, err)
	}
}
