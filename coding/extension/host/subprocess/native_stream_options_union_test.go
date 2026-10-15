package subprocess

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func TestNativeProviderOptionsKeepRawValuesAndNumericPresence(t *testing.T) {
	options := ai.StreamOptions{
		APIKey: "request-key", TimeoutMs: new(0), WebSocketConnectTimeoutMs: new(0), MaxRetries: new(0), MaxRetryDelayMs: new(17),
		ReasoningEffort: "high", ThinkingEnabled: new(false), ThinkingBudgetTokens: new(0), InterleavedThinking: new(false), ThinkingDisplay: ai.AnthropicThinkingDisplayOmitted,
		GoogleThinking: &ai.GoogleThinkingOptions{Enabled: true, BudgetTokens: new(0)}, RequestMetadata: map[string]string{"owner": "request"},
	}
	want := map[string]any{
		"apiKey": "request-key", "timeoutMs": 0, "websocketConnectTimeoutMs": 0, "maxRetries": 0, "maxRetryDelayMs": 17,
		"reasoningEffort": "high", "thinkingEnabled": false, "thinkingBudgetTokens": 0, "interleavedThinking": false, "thinkingDisplay": options.ThinkingDisplay,
		"thinking": options.GoogleThinking,
	}
	got := nativeStreamOptions(options)
	// requestMetadata is an ordered object on the wire (TestProviderOptionsWireKeepsRequestMetadataOrder).
	metadata, err := json.Marshal(got["requestMetadata"])
	if err != nil || string(metadata) != `{"owner":"request"}` {
		t.Fatalf("requestMetadata = %s (%v)", metadata, err)
	}
	delete(got, "requestMetadata")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("native options lost raw values or presence: got=%#v want=%#v", got, want)
	}
	if got := nativeStreamOptions(ai.StreamOptions{}); len(got) != 0 {
		t.Fatalf("absent native options were fabricated: %#v", got)
	}
}
