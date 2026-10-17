//go:build !pig_strip_bedrock_converse_stream

package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

// bedrock-converse-stream.ts:445-457 appendBedrockFailureDiagnostic assigns status, then errorCode, then requestId; JSON.stringify writes the details in that order, where a Go map sorted them.
func TestBedrockFailureDetailsKeepPisMemberOrder(t *testing.T) {
	message := bedrockHTTPFailure(t, t.Context(), 400, "ValidationException", bedrockFailureRequestID)
	encoded, err := json.Marshal(message.Diagnostics)
	if err != nil {
		t.Fatal(err)
	}
	if want := `"details":{"status":400,"errorCode":"ValidationException","requestId":"` + bedrockFailureRequestID + `"}`; !strings.Contains(string(encoded), want) {
		t.Errorf("diagnostics = %s, want %s", encoded, want)
	}
}
