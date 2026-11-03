package evalsuites

import (
	"encoding/json"
	"testing"
)

// documentation-audit.eval.ts:15-21 declares the schema with `Type.Object({ verdict, explanation: Type.String({ minLength: 1, maxLength: 2000 }) }, { additionalProperties: false })`:
// TypeBox writes type, required, properties and then the options, and JSON.stringify keeps that order. The verdict is ai.StringEnum, which writes type before enum.
func TestSubmitAuditParametersKeepTheirAuthoredOrder(t *testing.T) {
	encoded, err := json.Marshal(submitAudit().Parameters)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"type":"object","required":["verdict","explanation"],"properties":{"verdict":{"type":"string","enum":["match","mismatch","inconclusive"]},"explanation":{"type":"string","minLength":1,"maxLength":2000}},"additionalProperties":false}`
	if string(encoded) != want {
		t.Errorf("submit_documentation_audit parameters\n got: %s\nwant: %s", encoded, want)
	}
}
