package ai

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// system-one-shared.ts classifySystemOne builds its output with `timestamp: Date.now()` before the request, so every result, successful or failed, carries
// the time the classification started.
// mutation-checked: a zero or missing timestamp fails.
func TestClassifySystemOneStampsEveryResultWithItsStartTime(t *testing.T) {
	const api = ClassifierAPI("stamp-system-one")
	model := ClassifierModel{ID: "m", API: api, Provider: "custom", BaseURL: "https://example.test"}
	transport := SystemOneTransport{
		API:     api,
		Label:   "Stamp service",
		URL:     func(ClassifierModel) string { return "https://example.test/classify" },
		Payload: func(ClassifierModel, SystemOneWireRequest) map[string]any { return map[string]any{} },
		Output: func(body json.RawMessage) (map[string]json.RawMessage, error) {
			var envelope map[string]json.RawMessage
			return envelope, json.Unmarshal(body, &envelope)
		},
	}
	for name, response := range map[string]string{"success": `{"answers":{}}`, "malformed response": `[]`} {
		t.Run(name, func(t *testing.T) {
			before := time.Now().UnixMilli()
			result := ClassifySystemOne(t.Context(), transport, model, ClassifierContext{}, ClassifierOptions{APIKey: "k", Fetch: clsClient(func(*http.Request) (*http.Response, error) { return clsJSON(200, response), nil })})
			after := time.Now().UnixMilli()
			if result.Timestamp < before || result.Timestamp > after {
				t.Errorf("timestamp %d outside the call's window [%d, %d] (stop %v %q)", result.Timestamp, before, after, result.StopReason, result.ErrorMessage)
			}
		})
	}
	unsupported := model
	unsupported.API = "another-api"
	before := time.Now().UnixMilli()
	if result := ClassifySystemOne(t.Context(), transport, unsupported, ClassifierContext{}, ClassifierOptions{APIKey: "k"}); result.Timestamp < before {
		t.Errorf("a result that failed before any request has timestamp %d, want at least %d", result.Timestamp, before)
	}
}
