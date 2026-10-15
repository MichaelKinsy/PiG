package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

// Pi validation.ts:337-341 lists validator.Errors(args) for tool.parameters as the tool defined it, so a schema's properties are checked in their authored
// order. ValidateToolArguments must validate the decoded schema in that order, not in a Go map's sorted order.
func TestValidateToolArgumentsListsErrorsInTheSchemasMemberOrder(t *testing.T) {
	var tool ToolSchema
	if err := json.Unmarshal([]byte(`{"name":"pick","description":"d","parameters":{"type":"object","properties":{"zeta":{"type":"string"},"alpha":{"type":"string"}}}}`), &tool); err != nil {
		t.Fatal(err)
	}
	var call ToolCall
	if err := json.Unmarshal([]byte(`{"type":"toolCall","id":"c","name":"pick","arguments":{"alpha":[1],"zeta":[2]}}`), &call); err != nil {
		t.Fatal(err)
	}
	_, err := ValidateToolArguments(tool, call)
	if err == nil {
		t.Fatal("expected a validation error")
	}
	zeta, alpha := strings.Index(err.Error(), "  - zeta:"), strings.Index(err.Error(), "  - alpha:")
	if zeta < 0 || alpha < 0 || zeta > alpha {
		t.Errorf("errors not in the schema's member order (zeta, alpha):\n%s", err)
	}
}
