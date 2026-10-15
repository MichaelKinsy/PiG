package codingagent

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// The state pushed to an extension names the Session's current model: switching models between two pushes, and back, always reaches the extension, and the bytes equal the model's metadata encoded directly (runner.ts:906 `get model()` is live).
func TestExtensionStateFollowsTheSessionModelAcrossSwitches(t *testing.T) {
	small := ai.GetBuiltinModel("anthropic", "claude-haiku-4-5")
	large := ai.GetBuiltinModel("anthropic", "claude-sonnet-4-5")
	if small == nil || large == nil || small == large {
		t.Fatalf("catalog models: %v %v", small, large)
	}
	current := small
	bridge := subprocess.NewUIBridge(func() {})
	WireModelOperations(bridge, ModelOperationBindings{CurrentModel: func() *ai.Model { return current }})
	for _, model := range []*ai.Model{small, small, large, large, small, nil, small} {
		current = model
		wantBody := []byte("null")
		if model != nil {
			var err error
			if wantBody, err = extension.ModelObject(model); err != nil {
				t.Fatal(err)
			}
		}
		state, err := json.Marshal(bridge.Snapshot(nil, 0, false))
		if err != nil {
			t.Fatal(err)
		}
		var decoded struct {
			Model json.RawMessage `json:"model"`
		}
		if err := json.Unmarshal(state, &decoded); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(decoded.Model, wantBody) {
			t.Fatalf("model after switching to %v:\n got %s\nwant %s", model, decoded.Model, wantBody)
		}
	}
}
