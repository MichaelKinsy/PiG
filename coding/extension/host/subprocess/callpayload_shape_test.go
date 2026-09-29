package subprocess_test

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// CallPayload is exported, so external clients may build it with an unkeyed literal of its three fields. The literal below stops compiling if the struct gains, loses, or reorders a field, or gains an unexported field.
func TestCallPayloadKeepsUnkeyedLiteralShape(t *testing.T) {
	call := subprocess.CallPayload{"ui.setHeader", json.RawMessage(`{}`), "parent"}
	if call.Method != "ui.setHeader" || string(call.Args) != `{}` || call.ParentRequestID != "parent" {
		t.Fatalf("call = %#v", call)
	}
}
