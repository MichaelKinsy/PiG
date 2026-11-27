package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestClassifyPiDecisionAndOpenRouterClassifier(t *testing.T) {
	t.Run("posts POST to /decision with canonical model/context/options and parses answers, usage, responseId", func(t *testing.T) {
		var gotMethod, gotPath string
		var gotBody map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotMethod = r.Method
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
		sess := "sess-42"
		res := ClassifyPiDecision(context.Background(), model, req, PiDecisionOptions{
			APIKey: "tok", APIKeySet: true,
			SessionID: &sess,
		})
		if res.StopReason != ClassifierStopReasonStop {
			t.Fatalf("stopReason=%q err=%q", res.StopReason, res.ErrorMessage)
		}
		if gotMethod != http.MethodPost {
			t.Fatalf("method=%q, want POST", gotMethod)
		}
		if gotPath != "/v1/decision" {
			t.Fatalf("path=%q, want /v1/decision", gotPath)
		}
		if gotBody["model"] != "test-decision" {
			t.Fatalf("request model=%v, want test-decision", gotBody["model"])
		}
		reqCtx, _ := gotBody["context"].(map[string]any)
		if reqCtx == nil || reqCtx["state"] == nil || reqCtx["questions"] == nil {
			t.Fatalf("request context=%v", gotBody["context"])
		}
		opts, _ := gotBody["options"].(map[string]any)
		if opts == nil || opts["sessionId"] != "sess-42" {
			t.Fatalf("request options=%v, want sessionId=sess-42", gotBody["options"])
		}
		if res.ResponseID != "dec-resp-1" {
			t.Fatalf("responseId=%q, want dec-resp-1", res.ResponseID)
		}
		if res.Usage == nil || res.Usage.TotalTokens != 110 || res.Usage.Cost.Total != 0.001 || res.Usage.Cost.TotalFromProvider == nil || !*res.Usage.Cost.TotalFromProvider {
			t.Fatalf("usage=%+v", res.Usage)
		}
		wantAnswers := ClassifierAnswers{
			{ID: "route", Answer: ClassifierChoiceAnswer{Choice: "fast", Probabilities: []ClassifierProbability{{Key: "fast", Probability: 0.9}, {Key: "slow", Probability: 0.1}}, Confidence: 0.9}},
			{ID: "quality", Answer: ClassifierScoreAnswer{Score: 2, Confidence: 0.8}},
			{ID: "safe", Answer: ClassifierBoolAnswer{Probability: 0.95}},
		}
		if !reflect.DeepEqual(res.Answers, wantAnswers) {
			t.Fatalf("answers=%#v, want %#v", res.Answers, wantAnswers)
		}
	})

	t.Run("forwards explicit empty sessionId and omits options when SessionID is nil", func(t *testing.T) {
		var bodies []map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var b map[string]any
			_ = json.NewDecoder(r.Body).Decode(&b)
			bodies = append(bodies, b)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"answers":{"safe":{"type":"bool","probability":0.5}}}`))
		}))
		defer srv.Close()

		model := ClassifierModel{ID: "m", API: ClassifierAPIPiDecision, Provider: "radius", BaseURL: srv.URL, Input: []string{"text"}}
		req := ClassifierContext{State: JsonObject{"x": 1}, Questions: ClassifierQuestions{{ID: "safe", Question: ClassifierBoolQuestion{Instructions: "ok?", Criteria: ClassifierBoolCriteria{True: "y", False: "n"}}}}}
		_ = ClassifyPiDecision(context.Background(), model, req, PiDecisionOptions{APIKey: "k", APIKeySet: true})
		empty := ""
		_ = ClassifyPiDecision(context.Background(), model, req, PiDecisionOptions{APIKey: "k", APIKeySet: true, SessionID: &empty})
		if _, hasOpts := bodies[0]["options"]; hasOpts {
			t.Fatalf("nil SessionID should omit options, got %v", bodies[0]["options"])
		}
		opts, _ := bodies[1]["options"].(map[string]any)
		if opts == nil || opts["sessionId"] != "" {
			t.Fatalf("explicit empty SessionID should send options.sessionId=\"\", got %v", bodies[1]["options"])
		}
	})

	t.Run("forwards images on multimodal model and rejects on text-only model", func(t *testing.T) {
		var gotBody map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"answers":{"safe":{"type":"bool","probability":0.7}}}`))
		}))
		defer srv.Close()

		img := ImageContent{Data: "AA==", MimeType: "image/png"}
		req := ClassifierContext{
			State:     JsonObject{"x": 1},
			Images:    []ImageContent{img},
			Questions: ClassifierQuestions{{ID: "safe", Question: ClassifierBoolQuestion{Instructions: "ok?", Criteria: ClassifierBoolCriteria{True: "y", False: "n"}}}},
		}
		textOnly := ClassifierModel{ID: "text-only", API: ClassifierAPIPiDecision, Provider: "radius", BaseURL: srv.URL, Input: []string{"text"}}
		bad := ClassifyPiDecision(context.Background(), textOnly, req, PiDecisionOptions{})
		if bad.StopReason != ClassifierStopReasonError || !strings.Contains(bad.ErrorMessage, "does not support image input") {
			t.Fatalf("textOnly res=%+v", bad)
		}

		multimodal := ClassifierModel{ID: "vision", API: ClassifierAPIPiDecision, Provider: "radius", BaseURL: srv.URL, Input: []string{"text", "image"}}
		ok := ClassifyPiDecision(context.Background(), multimodal, req, PiDecisionOptions{APIKey: "k", APIKeySet: true})
		if ok.StopReason != ClassifierStopReasonStop {
			t.Fatalf("multimodal res=%+v", ok)
		}
		ctxObj, _ := gotBody["context"].(map[string]any)
		imgs, _ := ctxObj["images"].([]any)
		if len(imgs) != 1 {
			t.Fatalf("expected 1 forwarded image, got %v", ctxObj["images"])
		}
	})

	t.Run("handles 402 error envelope", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusPaymentRequired)
			_, _ = w.Write([]byte(`{"error":{"message":"Quota exceeded","code":"payment_required"}}`))
		}))
		defer srv.Close()

		model := ClassifierModel{ID: "m", API: ClassifierAPIPiDecision, Provider: "radius", BaseURL: srv.URL, Input: []string{"text"}}
		res := ClassifyPiDecision(context.Background(), model, ClassifierContext{
			State:     JsonObject{"x": 1},
			Questions: ClassifierQuestions{{ID: "safe", Question: ClassifierBoolQuestion{Instructions: "ok?", Criteria: ClassifierBoolCriteria{True: "y", False: "n"}}}},
		}, PiDecisionOptions{APIKey: "k", APIKeySet: true})
		if res.StopReason != ClassifierStopReasonError || !strings.Contains(res.ErrorMessage, "Quota exceeded") {
			t.Fatalf("402 res=%+v", res)
		}
	})

	t.Run("rejects unknown choice (invented, toString), null choice, and unknown probability key (bogus)", func(t *testing.T) {
		req := ClassifierContext{
			State: JsonObject{"x": 1},
			Questions: ClassifierQuestions{{
				ID: "route",
				Question: ClassifierChoiceQuestion{
					Instructions: "pick",
					Criteria:     []ClassifierChoiceCriterion{{Key: "fast", Description: "f"}, {Key: "slow", Description: "s"}},
				},
			}},
		}
		cases := []struct {
			name string
			body string
			want string
		}{
			{"invented choice", `{"answers":{"route":{"type":"choice","choice":"invented","probabilities":{"fast":1},"confidence":1}}}`, `Pi Decision returned unknown choice "invented" for route`},
			{"toString choice", `{"answers":{"route":{"type":"choice","choice":"toString","probabilities":{"fast":1},"confidence":1}}}`, `Pi Decision returned unknown choice "toString" for route`},
			{"null choice", `{"answers":{"route":{"type":"choice","choice":null,"probabilities":{"fast":1},"confidence":1}}}`, `Pi Decision did not return a choice answer for route`},
			{"bogus probability key", `{"answers":{"route":{"type":"choice","choice":"fast","probabilities":{"bogus":0.5},"confidence":1}}}`, `Pi Decision returned unknown choice "bogus" in probabilities for route`},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(tc.body))
				}))
				defer srv.Close()
				model := ClassifierModel{ID: "m", API: ClassifierAPIPiDecision, Provider: "radius", BaseURL: srv.URL, Input: []string{"text"}}
				res := ClassifyPiDecision(context.Background(), model, req, PiDecisionOptions{APIKey: "k", APIKeySet: true})
				if res.StopReason != ClassifierStopReasonError || res.ErrorMessage != tc.want {
					t.Fatalf("got stopReason=%q err=%q, want %q", res.StopReason, res.ErrorMessage, tc.want)
				}
			})
		}
	})

	t.Run("JSON null usage/cost fields are not treated as 0 or false (M2) and token counts clamp (L6)", func(t *testing.T) {
		model := ClassifierModel{ID: "m", API: ClassifierAPIPiDecision, Provider: "radius", Input: []string{"text"}, Cost: ModelCost{Input: 10}}

		// input: null, output: null => usage is nil
		if got := parsePiDecisionUsage(json.RawMessage(`{"input":null,"output":null}`), model); got != nil {
			t.Fatalf("null input/output usage = %+v, want nil", got)
		}

		// totalTokens: null => computed from input+output+cacheRead+cacheWrite, not 0
		got := parsePiDecisionUsage(json.RawMessage(`{"input":100,"output":25,"cacheRead":null,"cacheWrite":null,"totalTokens":null,"cost":{"total":null}}`), model)
		if got == nil || got.TotalTokens != 125 {
			t.Fatalf("null totalTokens usage = %+v, want TotalTokens=125", got)
		}
		// cost.total: null => falls back to CalculateCost(model, usage) = 100 * 10 / 1e6 = 0.001
		if got.Cost.Total != 0.001 {
			t.Fatalf("null cost.total should fall back to catalog cost 0.001, got %v", got.Cost.Total)
		}

		// cost.totalFromProvider: null => omitted (nil), not false
		gotNullFlag := parsePiDecisionUsage(json.RawMessage(`{"input":100,"output":0,"cost":{"total":0.5,"totalFromProvider":null}}`), model)
		if gotNullFlag == nil || gotNullFlag.Cost.Total != 0.5 || gotNullFlag.Cost.TotalFromProvider != nil {
			t.Fatalf("null totalFromProvider usage = %+v, want nil TotalFromProvider", gotNullFlag)
		}

		// Huge token count clamps to int(maxSafeInteger) via classifierTokenCount, and negative values clamp to 0
		gotHuge := parsePiDecisionUsage(json.RawMessage(`{"input":1e30,"output":-5}`), model)
		if gotHuge == nil || gotHuge.Input != int(maxSafeInteger) || gotHuge.Output != 0 {
			t.Fatalf("huge/negative token count = %+v, want Input=%d Output=0", gotHuge, int(maxSafeInteger))
		}
	})

	t.Run("uses JS key order for integer-like question and probability keys (M3)", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"answers": {
					"zeta": {"type": "choice", "choice": "2", "probabilities": {"b": 0.2, "10": 0.3, "2": 0.5}, "confidence": 0.5},
					"10": {"type": "bool", "probability": 0.4},
					"2": {"type": "bool", "probability": 0.6}
				}
			}`))
		}))
		defer srv.Close()

		model := ClassifierModel{ID: "m", API: ClassifierAPIPiDecision, Provider: "radius", BaseURL: srv.URL, Input: []string{"text"}}
		req := ClassifierContext{
			State: JsonObject{"x": 1},
			Questions: ClassifierQuestions{
				{ID: "zeta", Question: ClassifierChoiceQuestion{Instructions: "pick", Criteria: []ClassifierChoiceCriterion{{Key: "b", Description: "b"}, {Key: "10", Description: "10"}, {Key: "2", Description: "2"}}}},
				{ID: "10", Question: ClassifierBoolQuestion{Instructions: "ten?", Criteria: ClassifierBoolCriteria{True: "y", False: "n"}}},
				{ID: "2", Question: ClassifierBoolQuestion{Instructions: "two?", Criteria: ClassifierBoolCriteria{True: "y", False: "n"}}},
			},
		}
		res := ClassifyPiDecision(context.Background(), model, req, PiDecisionOptions{APIKey: "k", APIKeySet: true})
		if res.StopReason != ClassifierStopReasonStop {
			t.Fatalf("stopReason=%q err=%q", res.StopReason, res.ErrorMessage)
		}
		gotIDs := []string{res.Answers[0].ID, res.Answers[1].ID, res.Answers[2].ID}
		wantIDs := []string{"2", "10", "zeta"}
		if !reflect.DeepEqual(gotIDs, wantIDs) {
			t.Fatalf("answer key order = %v, want JS order %v", gotIDs, wantIDs)
		}
		choiceAns := res.Answers[2].Answer.(ClassifierChoiceAnswer)
		gotProbKeys := []string{choiceAns.Probabilities[0].Key, choiceAns.Probabilities[1].Key, choiceAns.Probabilities[2].Key}
		wantProbKeys := []string{"2", "10", "b"}
		if !reflect.DeepEqual(gotProbKeys, wantProbKeys) {
			t.Fatalf("probability key order = %v, want JS order %v", gotProbKeys, wantProbKeys)
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

		model := ClassifierModel{ID: "test-decision", API: ClassifierAPIPiDecision, Provider: "radius", BaseURL: srv.URL, Input: []string{"text"}, Cost: ModelCost{Input: 1.0}}
		res := ClassifyPiDecision(context.Background(), model, ClassifierContext{
			State:     JsonObject{"x": 1},
			Questions: ClassifierQuestions{{ID: "safe", Question: ClassifierBoolQuestion{Instructions: "safe?", Criteria: ClassifierBoolCriteria{True: "y", False: "n"}}}},
		}, PiDecisionOptions{APIKey: "tok", APIKeySet: true})
		if res.StopReason != ClassifierStopReasonError || res.ErrorMessage != "Pi Decision returned out-of-range probability for safe: 1.5" {
			t.Fatalf("res=%+v", res)
		}
		if res.Usage == nil || res.Usage.Input != 50 {
			t.Fatalf("expected usage preserved on malformed answer, got %+v", res.Usage)
		}
	})

	t.Run("OpenRouter typesafe-system-one exposes responseId and reported cost (bare, BYOK sum, native fallback)", func(t *testing.T) {
		req := ClassifierContext{
			State:     JsonObject{"x": 1},
			Questions: ClassifierQuestions{{ID: "safe", Question: ClassifierBoolQuestion{Instructions: "safe?", Criteria: ClassifierBoolCriteria{True: "y", False: "n"}}}},
		}
		cases := []struct {
			name             string
			provider         string
			usageJSON        string
			wantCost         float64
			wantFromProvider bool
		}{
			{
				name:             "OpenRouter non-BYOK reported cost",
				provider:         "openrouter",
				usageJSON:        `{"input_tokens": 1000, "output_tokens": 0, "cost": 0.0042, "is_byok": false}`,
				wantCost:         0.0042,
				wantFromProvider: true,
			},
			{
				name:             "OpenRouter bare cost without is_byok falls back to catalog price",
				provider:         "openrouter",
				usageJSON:        `{"input_tokens": 1000000, "output_tokens": 0, "cost": 0.0042}`,
				wantCost:         0.02,
				wantFromProvider: false,
			},
			{
				name:             "OpenRouter BYOK sums cost and upstream_inference_cost",
				provider:         "openrouter",
				usageJSON:        `{"input_tokens": 1000, "output_tokens": 0, "cost": 0.001, "is_byok": true, "cost_details": {"upstream_inference_cost": 0.003}}`,
				wantCost:         0.004,
				wantFromProvider: true,
			},
			{
				name:             "native TypeSafe ignores OpenRouter reported cost and uses catalog price",
				provider:         "typesafe",
				usageJSON:        `{"input_tokens": 1000000, "output_tokens": 0, "cost": 99.0, "is_byok": false}`,
				wantCost:         0.02,
				wantFromProvider: false,
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"id":"gen-or-123","answers":{"safe":{"type":"noul","noul":0.8}},"usage":` + tc.usageJSON + `}`))
				}))
				defer srv.Close()

				model := ClassifierModel{ID: "liquid/d1", API: ClassifierAPITypesafeSystemOne, Provider: tc.provider, BaseURL: srv.URL, Input: []string{"text"}, Cost: ModelCost{Input: 0.02}}
				res := ClassifyTypesafeSystemOne(context.Background(), model, req, ClassifierOptions{APIKey: "tok", APIKeySet: true})
				if res.StopReason != ClassifierStopReasonStop {
					t.Fatalf("stopReason=%q err=%q", res.StopReason, res.ErrorMessage)
				}
				if res.ResponseID != "gen-or-123" {
					t.Fatalf("responseId=%q, want gen-or-123", res.ResponseID)
				}
				if res.Usage == nil || res.Usage.Cost.Total != tc.wantCost {
					t.Fatalf("cost.total=%v, want %v (usage=%+v)", res.Usage.Cost.Total, tc.wantCost, res.Usage)
				}
				gotFromProvider := res.Usage.Cost.TotalFromProvider != nil && *res.Usage.Cost.TotalFromProvider
				if gotFromProvider != tc.wantFromProvider {
					t.Fatalf("totalFromProvider=%v, want %v", gotFromProvider, tc.wantFromProvider)
				}
			})
		}
	})
}
