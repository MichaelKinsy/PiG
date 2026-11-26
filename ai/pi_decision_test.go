package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClassifyPiDecisionAndOpenRouterClassifier(t *testing.T) {
	t.Run("posts to /decision with canonical body and parses answers, usage, responseId", func(t *testing.T) {
		var gotPath string
		var gotBody map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
				t.Errorf("decode request: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"responseId": "dec-resp-1",
				"answers": {
					"route": {"type": "choice", "choice": "fast", "probabilities": {"fast": 0.9, "slow": 0.1}, "confidence": 0.9},
					"quality": {"type": "score", "score": 2, "confidence": 0.8},
					"safe": {"type": "bool", "probability": 0.95}
				},
				"usage": {
					"input": 100,
					"output": 10,
					"cacheRead": 0,
					"cacheWrite": 0,
					"totalTokens": 110,
					"cost": {"input": 0.001, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0.001, "totalFromProvider": true}
				}
			}`))
		}))
		defer srv.Close()

		model := ClassifierModel{
			ID:       "test-decision",
			API:      ClassifierAPIPiDecision,
			Provider: "radius",
			BaseURL:  srv.URL + "/v1/",
			Input:    []string{"text"},
			Cost:     ModelCost{Input: 0.1},
		}
		req := ClassifierContext{
			State: JsonObject{"prompt": "hi"},
			Questions: ClassifierQuestions{
				{ID: "route", Question: ClassifierChoiceQuestion{Instructions: "pick", Criteria: []ClassifierChoiceCriterion{{Key: "fast", Description: "f"}, {Key: "slow", Description: "s"}}}},
				{ID: "quality", Question: ClassifierScoreQuestion{Instructions: "rate", Criteria: []string{"bad", "ok", "good"}}},
				{ID: "safe", Question: ClassifierBoolQuestion{Instructions: "safe?", Criteria: ClassifierBoolCriteria{True: "yes", False: "no"}}},
			},
		}
		res := ClassifyPiDecision(context.Background(), model, req, PiDecisionOptions{
			ClassifierOptions: ClassifierOptions{APIKey: "tok", APIKeySet: true},
			SessionID:         "sess-42",
		})
		if res.StopReason != ClassifierStopReasonStop {
			t.Fatalf("stopReason=%q err=%q", res.StopReason, res.ErrorMessage)
		}
		if gotPath != "/v1/decision" {
			t.Fatalf("path=%q, want /v1/decision", gotPath)
		}
		if res.ResponseID != "dec-resp-1" {
			t.Fatalf("responseId=%q, want dec-resp-1", res.ResponseID)
		}
		if res.Usage == nil || res.Usage.TotalTokens != 110 || res.Usage.Cost.TotalFromProvider == nil || !*res.Usage.Cost.TotalFromProvider {
			t.Fatalf("usage=%+v", res.Usage)
		}
		if len(res.Answers) != 3 {
			t.Fatalf("answers=%+v", res.Answers)
		}
		opts, _ := gotBody["options"].(map[string]any)
		if opts == nil || opts["sessionId"] != "sess-42" {
			t.Fatalf("request options=%v, want sessionId=sess-42", gotBody["options"])
		}
	})

	t.Run("rejects images when model does not support image input", func(t *testing.T) {
		model := ClassifierModel{
			ID:       "text-only",
			API:      ClassifierAPIPiDecision,
			Provider: "radius",
			BaseURL:  "http://127.0.0.1:1",
			Input:    []string{"text"},
		}
		res := ClassifyPiDecision(context.Background(), model, ClassifierContext{
			State:  JsonObject{"x": 1},
			Images: []ImageContent{{Data: "AA==", MimeType: "image/png"}},
		}, PiDecisionOptions{})
		if res.StopReason != ClassifierStopReasonError || !strings.Contains(res.ErrorMessage, "does not support image input") {
			t.Fatalf("res=%+v", res)
		}
	})

	t.Run("rejects out-of-range probability and preserves usage on malformed answer", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"usage": {"input": 50, "output": 0},
				"answers": {"safe": {"type": "bool", "probability": 1.5}}
			}`))
		}))
		defer srv.Close()

		model := ClassifierModel{
			ID:       "test-decision",
			API:      ClassifierAPIPiDecision,
			Provider: "radius",
			BaseURL:  srv.URL,
			Input:    []string{"text"},
			Cost:     ModelCost{Input: 1.0},
		}
		res := ClassifyPiDecision(context.Background(), model, ClassifierContext{
			State:     JsonObject{"x": 1},
			Questions: ClassifierQuestions{{ID: "safe", Question: ClassifierBoolQuestion{Instructions: "safe?", Criteria: ClassifierBoolCriteria{True: "y", False: "n"}}}},
		}, PiDecisionOptions{ClassifierOptions: ClassifierOptions{APIKey: "tok", APIKeySet: true}})
		if res.StopReason != ClassifierStopReasonError || !strings.Contains(res.ErrorMessage, "out-of-range") {
			t.Fatalf("res=%+v", res)
		}
		if res.Usage == nil || res.Usage.Input != 50 {
			t.Fatalf("expected usage preserved on malformed answer, got %+v", res.Usage)
		}
	})

	t.Run("OpenRouter typesafe-system-one exposes responseId and reported cost", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"id": "gen-or-123",
				"answers": {"safe": {"type": "noul", "noul": 0.8}},
				"usage": {"input_tokens": 1000, "output_tokens": 0, "cost": 0.0042, "is_byok": false}
			}`))
		}))
		defer srv.Close()

		model := ClassifierModel{
			ID:       "liquid/d1",
			API:      ClassifierAPITypesafeSystemOne,
			Provider: "openrouter",
			BaseURL:  srv.URL,
			Input:    []string{"text"},
			Cost:     ModelCost{Input: 0.02},
		}
		res := ClassifyTypesafeSystemOne(context.Background(), model, ClassifierContext{
			State:     JsonObject{"x": 1},
			Questions: ClassifierQuestions{{ID: "safe", Question: ClassifierBoolQuestion{Instructions: "safe?", Criteria: ClassifierBoolCriteria{True: "y", False: "n"}}}},
		}, ClassifierOptions{APIKey: "tok", APIKeySet: true})
		if res.StopReason != ClassifierStopReasonStop {
			t.Fatalf("stopReason=%q err=%q", res.StopReason, res.ErrorMessage)
		}
		if res.ResponseID != "gen-or-123" {
			t.Fatalf("responseId=%q, want gen-or-123", res.ResponseID)
		}
		if res.Usage == nil || res.Usage.Cost.Total != 0.0042 || res.Usage.Cost.TotalFromProvider == nil || !*res.Usage.Cost.TotalFromProvider {
			t.Fatalf("usage=%+v", res.Usage)
		}
	})
}
