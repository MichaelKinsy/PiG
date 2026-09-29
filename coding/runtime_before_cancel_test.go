package coding

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Subprocess extension handlers return their session_before_switch and session_before_fork results as JSON, not as the typed result. Runtime replacement must still honor `cancel: true` (agent-session-runtime.ts emitBeforeSwitch and emitBeforeFork read result?.cancel === true).
func TestRuntimeCancelsThroughJSONBeforeResults(t *testing.T) {
	cancelled := json.RawMessage(`{"cancel":true}`)
	proceeds := json.RawMessage(`{"cancel":false}`)
	ext := func() extension.Extension {
		return extension.Extension{Handlers: map[string][]extension.HandlerFn{
			"session_before_switch": {func(...any) (any, error) { return cancelled, nil }},
			"session_before_fork":   {func(...any) (any, error) { return cancelled, nil }},
		}}
	}
	h := newRuntimeTestHarness(t, runtimeTestOptions{extension: ext})
	runtimePrompt(t, h.runtime, "hello")
	original := h.runtime.Session()
	if result, err := h.runtime.NewSession(t.Context(), nil); err != nil || !result.Cancelled || h.runtime.Session() != original {
		t.Fatalf("new with a JSON cancel = %v, %v; want a cancelled replacement that keeps the Session", result, err)
	}
	entry := original.SessionManager().GetBranch()[0]
	if result, err := h.runtime.Fork(t.Context(), entry.Base.ID, nil); err != nil || !result.Cancelled || h.runtime.Session() != original {
		t.Fatalf("fork with a JSON cancel = %v, %v; want a cancelled fork that keeps the Session", result, err)
	}
	if beforeResultCancelled(proceeds) || beforeResultCancelled(nil) || beforeResultCancelled(json.RawMessage(`not json`)) {
		t.Fatal("a result without cancel true must not cancel")
	}
}
