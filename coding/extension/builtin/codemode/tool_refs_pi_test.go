package codemode

import (
	"encoding/json"
	"testing"
)

// Ports packages/coding-agent/test/suite/agent-session-codemode.test.ts "summarizes output fields behind local schema references" (Pi 6d3e342b2,
// #11803): the `resolves to` summary of describeOutput follows local `$ref`s through the root, a field, a const, and the variants of a union.
func TestDescribeOutputSummarizesFieldsBehindLocalSchemaReferences(t *testing.T) {
	report := json.RawMessage(`{"$ref":"#/$defs/Report","$defs":{"Report":{"type":"object","properties":{"kind":{"$ref":"#/$defs/Kind"},"summary":{"$ref":"#/$defs/Summary"}},"required":["kind","summary"]},"Kind":{"const":"ledger"},"Summary":{"type":"object","properties":{"total":{"type":"number"}},"required":["total"]}}}`)
	got, err := describeOutput("report", report)
	if err != nil || got != "`{ kind: \"ledger\", summary: { total } }`" {
		t.Fatalf("report = %q, %v", got, err)
	}
	result := json.RawMessage(`{"anyOf":[{"$ref":"#/$defs/Ok"},{"$ref":"#/$defs/Failed"}],"$defs":{"Ok":{"type":"object","properties":{"value":{"type":"string"}},"required":["value"]},"Failed":{"type":"object","properties":{"error":{"type":"string"}},"required":["error"]}}}`)
	got, err = describeOutput("result", result)
	if err != nil || got != "`{ value } | { error }`" {
		t.Fatalf("result = %q, %v", got, err)
	}
}
