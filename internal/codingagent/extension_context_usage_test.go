package codingagent

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi interactive-mode.ts delegates getContextUsage to AgentSession, whose projection includes estimates without assistant usage (agent-session.ts:3858-3900).
func TestExtensionContextUsageUsesSessionProjection(t *testing.T) {
	bridge := &captureUIBridge{}
	var tokens *int
	window := 0
	m := &InteractiveMode{opts: InteractiveModeOptions{SubprocessUIBridge: bridge, ContextUsage: func() (*int, int) { return tokens, window }}}
	detach := m.wireSubprocessHostCallbacks()
	defer detach()
	get := bridge.actions["getContextUsage"].(func() *extension.ContextUsage)
	for _, tc := range []struct {
		tokens *int
		window int
		want   string
	}{
		{nil, 0, `null`},
		{new(int), 128000, `{"tokens":0,"contextWindow":128000,"percent":0}`},
		{nil, 128000, `{"tokens":null,"contextWindow":128000,"percent":null}`},
		{new(int), 128000, `{"tokens":0,"contextWindow":128000,"percent":0}`},
	} {
		tokens, window = tc.tokens, tc.window
		raw, err := json.Marshal(get())
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != tc.want {
			t.Errorf("usage = %s, want %s", raw, tc.want)
		}
	}
	estimate := 15
	tokens = &estimate
	raw, err := json.Marshal(get())
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"tokens":15,"contextWindow":128000,"percent":0.01171875}`; string(raw) != want {
		t.Errorf("usage = %s, want %s", raw, want)
	}
}
