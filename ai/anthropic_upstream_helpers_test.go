package ai

import (
	"encoding/json"
	"errors"
	"testing"
)

// captureAnthropicUpstreamPayload stops at the onPayload boundary and asserts Pi's terminal error result.
func captureAnthropicUpstreamPayload(t *testing.T, provider Provider, input Context, options StreamOptions) map[string]json.RawMessage {
	t.Helper()
	defer func() {
		if err := provider.Close(); err != nil {
			t.Error(err)
		}
	}()
	captured := errors.New("payload captured")
	var payload map[string]json.RawMessage
	options.OnPayload = func(value any, _ *Model) (any, error) {
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(encoded, &payload); err != nil {
			return nil, err
		}
		return nil, captured
	}
	stream, err := provider.Stream(t.Context(), NormalizeContext(input), options)
	result := requireAnthropicSetupError(t, stream, err)
	if result.ErrorMessage != captured.Error() {
		t.Fatalf("onPayload rejection = %q; want captured sentinel", result.ErrorMessage)
	}
	if payload == nil {
		t.Fatal("onPayload was not called")
	}
	return payload
}
