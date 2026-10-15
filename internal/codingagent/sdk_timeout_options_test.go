package codingagent

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi: packages/coding-agent/src/core/settings-manager.ts:1042 (SettingsManager.getWebSocketConnectTimeoutMs).
func TestWebSocketConnectTimeoutSettingPresence(t *testing.T) {
	for _, tc := range []struct {
		raw     string
		want    *int
		invalid bool
	}{
		{`{}`, nil, false}, {`{"websocketConnectTimeoutMs":0}`, new(0), false}, {`{"websocketConnectTimeoutMs":"disabled"}`, new(0), false}, {`{"websocketConnectTimeoutMs":"1234.9"}`, new(1234), false},
		{`{"websocketConnectTimeoutMs":null}`, nil, true}, {`{"websocketConnectTimeoutMs":-1}`, nil, true}, {`{"websocketConnectTimeoutMs":"bad"}`, nil, true},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			var settings Settings
			if err := json.Unmarshal([]byte(tc.raw), &settings); err != nil {
				t.Fatal(err)
			}
			manager := &SettingsManager{merged: settings}
			got, err := manager.GetWebSocketConnectTimeoutMs()
			if (err != nil) != tc.invalid {
				t.Fatalf("value=%v error=%v", got, err)
			}
			if !tc.invalid && ((got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want)) {
				t.Fatalf("value=%v want=%v", got, tc.want)
			}
			data, err := json.Marshal(settings)
			if err != nil {
				t.Fatal(err)
			}
			var restored Settings
			if err := json.Unmarshal(data, &restored); err != nil {
				t.Fatal(err)
			}
			manager.merged = restored
			_, err = manager.GetWebSocketConnectTimeoutMs()
			if (err != nil) != tc.invalid {
				t.Fatalf("round trip %s lost invalid/present state", data)
			}
		})
	}
}
func TestSubprocessStreamOptionsPreserveTimeoutAndRetryPresence(t *testing.T) {
	defaults := ai.StreamOptions{TimeoutMs: new(1234), WebSocketConnectTimeoutMs: new(4321), MaxRetries: new(2), MaxRetryDelayMs: new(3000)}
	options, err := subprocessStreamOptions(map[string]any{"timeoutMs": float64(0), "websocketConnectTimeoutMs": float64(0), "maxRetries": float64(0), "maxRetryDelayMs": float64(0)}, defaults)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []*int{options.TimeoutMs, options.WebSocketConnectTimeoutMs, options.MaxRetries, options.MaxRetryDelayMs} {
		if value == nil || *value != 0 {
			t.Fatalf("explicit zero lost: %+v", options)
		}
	}
	options, err = subprocessStreamOptions(map[string]any{}, defaults)
	if err != nil {
		t.Fatal(err)
	}
	if *options.TimeoutMs != 1234 || *options.WebSocketConnectTimeoutMs != 4321 || *options.MaxRetries != 2 || *options.MaxRetryDelayMs != 3000 {
		t.Fatalf("omission replaced defaults: %+v", options)
	}
	for _, key := range []string{"timeoutMs", "websocketConnectTimeoutMs", "maxRetries", "maxRetryDelayMs"} {
		if _, err := subprocessStreamOptions(map[string]any{key: float64(-1)}, defaults); err == nil {
			t.Fatalf("accepted negative %s", key)
		}
	}
}
