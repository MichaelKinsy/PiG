package ai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

// Google api/google-generative-ai.ts:402-412 preserves raw thinking config, with level taking precedence over a supplied budget.
func TestGoogleRawThinkingOptions(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want map[string]any
	}{
		{`{"thinking":{"enabled":true}}`, map[string]any{"includeThoughts": true}},
		{`{"thinking":{"enabled":true,"budgetTokens":1024}}`, map[string]any{"includeThoughts": true, "thinkingBudget": float64(1024)}},
		{`{"thinking":{"enabled":true,"budgetTokens":0}}`, map[string]any{"includeThoughts": true, "thinkingBudget": float64(0)}},
		{`{"thinking":{"enabled":true,"budgetTokens":1024,"level":"LOW"}}`, map[string]any{"includeThoughts": true, "thinkingLevel": "LOW"}},
		{`{"thinking":{"enabled":false}}`, map[string]any{"thinkingBudget": float64(0)}},
		{`{"reasoning":"high"}`, map[string]any{"includeThoughts": true, "thinkingBudget": float64(32768)}},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			var options StreamOptions
			if err := json.Unmarshal([]byte(tc.raw), &options); err != nil {
				t.Fatal(err)
			}
			options.IsReasoning = true
			captured := make(chan any, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				cfg, _ := body["generationConfig"].(map[string]any)
				captured <- cfg["thinkingConfig"]
				writeMatrixUsageResponse(t, w, APIGoogleGenerativeAI, "ok", false)
			}))
			defer server.Close()
			provider := NewGoogleProvider(GoogleConfig{APIKey: "test", Model: "gemini-2.5-pro", ProviderID: "google", BaseURL: server.URL})
			stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}), options)
			if err != nil {
				t.Fatal(err)
			}
			if result := stream.Result(); result.StopReason != StopReasonStop {
				t.Fatalf("result = %+v", result)
			}
			if got := <-captured; !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("thinkingConfig = %#v, want %#v", got, tc.want)
			}
		})
	}
}
