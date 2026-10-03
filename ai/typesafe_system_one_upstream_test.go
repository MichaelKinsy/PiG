package ai

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func typesafeTestModel() ClassifierModel {
	return ClassifierModel{ID: "jev-latest", Name: "Jev", API: "typesafe-system-one", Provider: "typesafe", BaseURL: "https://api.typesafe.ai/v1/", Input: []string{"text"}, ContextWindow: 64000}
}

func typesafeTestContext() ClassifierContext {
	return ClassifierContext{
		State: JsonObject{"text": "The deployment succeeded, thank you."},
		Questions: ClassifierQuestions{
			{ID: "category", Question: ClassifierChoiceQuestion{Instructions: "Classify the message", Criteria: clsChoices("success", "Successful", "failure", "Failed")}},
			{ID: "satisfaction", Question: ClassifierScoreQuestion{Instructions: "Score satisfaction", Criteria: []string{"low", "neutral", "high"}}},
			{ID: "approved", Question: ClassifierBoolQuestion{Instructions: "Does the user approve?", Criteria: ClassifierBoolCriteria{True: "Approval", False: "No approval"}}},
		},
	}
}

const typesafeWireAnswers = `{"category":{"type":"choice","choice":"success","probabilities":{"success":0.9,"failure":0.1},"confidence":0.8},"satisfaction":{"type":"score","score":2,"confidence":0.7},"approved":{"type":"noul","noul":0.95}}`

// Ports packages/ai/test/typesafe-system-one.test.ts.
func TestTypesafeSystemOneUpstream(t *testing.T) {
	// .upstream/v0.99.1/packages/ai/test/typesafe-system-one.test.ts:50
	t.Run("maps public bool questions and answers to TypeSafe noul values", func(t *testing.T) {
		model, request := typesafeTestModel(), typesafeTestContext()
		var urls []string
		fetch := clsClient(func(r *http.Request) (*http.Response, error) {
			urls = append(urls, r.URL.String())
			payload := clsBody(t, r)
			questions := payload["questions"].(map[string]any)
			if payload["model"] != "jev-latest" || questions["category"].(map[string]any)["type"] != "choice" || questions["satisfaction"].(map[string]any)["type"] != "score" || questions["approved"].(map[string]any)["type"] != "noul" {
				t.Errorf("payload=%v", payload)
			}
			// System One has no temperature field; the option is ignored.
			if _, ok := payload["temperature"]; ok {
				t.Error("payload has temperature")
			}
			if r.Header.Get("authorization") != "Bearer secret" {
				t.Errorf("authorization=%q", r.Header.Get("authorization"))
			}
			return clsJSON(200, `{"answers":`+typesafeWireAnswers+`}`), nil
		})

		result := ClassifyTypesafeSystemOne(t.Context(), model, request, ClassifierOptions{APIKey: "secret", Fetch: fetch, Temperature: new(1.5)})
		priced := model
		priced.Cost.Input = 0.042
		pricedResult := ClassifyTypesafeSystemOne(t.Context(), priced, request, ClassifierOptions{APIKey: "secret", Fetch: clsClient(func(*http.Request) (*http.Response, error) {
			return clsJSON(200, `{"answers":`+typesafeWireAnswers+`,"usage":{"input_tokens":308,"output_tokens":23}}`), nil
		})})

		if len(urls) != 1 || urls[0] != "https://api.typesafe.ai/v1/systemone" {
			t.Fatalf("urls=%v", urls)
		}
		if result.StopReason != "stop" {
			t.Fatalf("result=%+v", result)
		}
		if got := clsAnswer(t, result.Answers, "approved"); got != (ClassifierBoolAnswer{Probability: 0.95}) {
			t.Errorf("approved=%+v", got)
		}
		if got, ok := clsAnswer(t, result.Answers, "category").(ClassifierChoiceAnswer); !ok || got.Choice != "success" {
			t.Errorf("category=%+v", got)
		}
		if got := clsAnswer(t, result.Answers, "satisfaction"); got != (ClassifierScoreAnswer{Score: 2, Confidence: 0.7}) {
			t.Errorf("satisfaction=%+v", got)
		}
		if result.Usage != nil {
			t.Errorf("usage=%+v", result.Usage)
		}
		if pricedResult.Usage == nil || pricedResult.Usage.Input != 308 || pricedResult.Usage.Output != 23 || pricedResult.Usage.TotalTokens != 331 || math.Abs(pricedResult.Usage.Cost.Total-0.000012936) > 5e-13 {
			t.Errorf("priced usage=%+v", pricedResult.Usage)
		}
	})
	// .upstream/v0.99.1/packages/ai/test/typesafe-system-one.test.ts:83
	t.Run("posts OpenRouter System One requests to its TypeSafe-compatible endpoint", func(t *testing.T) {
		var requested string
		fetch := clsClient(func(r *http.Request) (*http.Response, error) {
			requested = r.URL.String()
			payload := clsBody(t, r)
			if payload["model"] != "typesafe/jev-1.13" || !reflect.DeepEqual(payload["state"], map[string]any{"text": "The deployment succeeded, thank you."}) {
				t.Errorf("payload=%v", payload)
			}
			// Response shape observed from the live OpenRouter endpoint.
			return clsJSON(200, `{"id":"gen-dec-1","provider":"TypeSafe","answers":`+typesafeWireAnswers+`,"usage":{"input_tokens":308,"output_tokens":23,"cost":0.000012936}}`), nil
		})
		model := typesafeTestModel()
		model.ID, model.Provider, model.BaseURL, model.Cost.Input = "typesafe/jev-1.13", "openrouter", "https://openrouter.ai/api/v1", 0.042

		result := ClassifyTypesafeSystemOne(t.Context(), model, typesafeTestContext(), ClassifierOptions{APIKey: "secret", Fetch: fetch})

		if requested != "https://openrouter.ai/api/v1/systemone" {
			t.Errorf("url=%s", requested)
		}
		if result.StopReason != "stop" || clsAnswer(t, result.Answers, "approved") != (ClassifierBoolAnswer{Probability: 0.95}) {
			t.Fatalf("result=%+v", result)
		}
		// Priced from the catalog like chat usage; matches OpenRouter's reported cost.
		if result.Usage == nil || math.Abs(result.Usage.Cost.Total-0.000012936) > 5e-13 {
			t.Errorf("usage=%+v", result.Usage)
		}
	})
	// .upstream/v0.99.1/packages/ai/test/typesafe-system-one.test.ts:111
	t.Run("rejects models for other classifier APIs", func(t *testing.T) {
		calls := 0
		fetch := clsClient(func(*http.Request) (*http.Response, error) {
			calls++
			return clsJSON(200, `{"answers":`+typesafeWireAnswers+`}`), nil
		})
		model := typesafeTestModel()
		model.API = "cloudflare-workers-ai-system-one"

		result := ClassifyTypesafeSystemOne(t.Context(), model, typesafeTestContext(), ClassifierOptions{APIKey: "secret", Fetch: fetch})

		if calls != 0 {
			t.Error("fetch was called")
		}
		if result.StopReason != "error" || !strings.Contains(result.ErrorMessage, "Unsupported classifier API: cloudflare-workers-ai-system-one") {
			t.Errorf("result=%+v", result)
		}
	})
	// .upstream/v0.99.1/packages/ai/test/typesafe-system-one.test.ts:123
	t.Run("merges headers case-insensitively and supports null suppression", func(t *testing.T) {
		var requests []http.Header
		fetch := clsClient(func(r *http.Request) (*http.Response, error) {
			requests = append(requests, r.Header.Clone())
			return clsJSON(200, `{"answers":`+typesafeWireAnswers+`}`), nil
		})
		model := typesafeTestModel()
		model.Headers = map[string]string{"authorization": "Bearer model", "X-Source": "model"}

		ClassifyTypesafeSystemOne(t.Context(), model, typesafeTestContext(), ClassifierOptions{APIKey: "secret", Fetch: fetch, Headers: ProviderHeaders{"Authorization": new("Bearer request"), "x-source": new("request")}})
		ClassifyTypesafeSystemOne(t.Context(), model, typesafeTestContext(), ClassifierOptions{APIKey: "secret", Fetch: fetch, Headers: ProviderHeaders{"Authorization": nil}})

		if len(requests) != 2 {
			t.Fatalf("requests=%d", len(requests))
		}
		if requests[0].Get("authorization") != "Bearer request" || requests[0].Get("x-source") != "request" {
			t.Errorf("headers=%v", requests[0])
		}
		count := 0
		for name := range requests[0] {
			if strings.EqualFold(name, "authorization") {
				count++
			}
		}
		if count != 1 {
			t.Errorf("authorization headers=%d", count)
		}
		if _, ok := requests[1]["Authorization"]; ok {
			t.Errorf("suppressed header sent: %v", requests[1])
		}
	})
	// .upstream/v0.99.1/packages/ai/test/typesafe-system-one.test.ts:150
	t.Run("preserves prototype-sensitive question IDs in answers", func(t *testing.T) {
		request := ClassifierContext{State: JsonObject{}, Questions: ClassifierQuestions{{ID: "__proto__", Question: ClassifierBoolQuestion{Instructions: "Is this true?", Criteria: ClassifierBoolCriteria{True: "Yes", False: "No"}}}}}

		result := ClassifyTypesafeSystemOne(t.Context(), typesafeTestModel(), request, ClassifierOptions{APIKey: "secret", Fetch: clsClient(func(*http.Request) (*http.Response, error) {
			return clsJSON(200, `{"answers":{"__proto__":{"type":"noul","noul":0.75}}}`), nil
		})})

		serialized, err := json.Marshal(result.Answers)
		if err != nil {
			t.Fatal(err)
		}
		var serializedAnswers map[string]any
		if err := json.Unmarshal(serialized, &serializedAnswers); err != nil {
			t.Fatal(err)
		}
		if result.StopReason != "stop" || !reflect.DeepEqual(clsAnswerIDs(result.Answers), []string{"__proto__"}) {
			t.Fatalf("result=%+v", result)
		}
		if got := clsAnswer(t, result.Answers, "__proto__"); got != (ClassifierBoolAnswer{Probability: 0.75}) {
			t.Errorf("answer=%+v", got)
		}
		if !reflect.DeepEqual(serializedAnswers["__proto__"], map[string]any{"type": "bool", "probability": 0.75}) {
			t.Errorf("serialized=%s", serialized)
		}
	})
	// .upstream/v0.99.1/packages/ai/test/typesafe-system-one.test.ts:170
	t.Run("reports request timeouts separately from caller cancellation", func(t *testing.T) {
		result := ClassifyTypesafeSystemOne(t.Context(), typesafeTestModel(), typesafeTestContext(), ClassifierOptions{APIKey: "secret", TimeoutMs: new(5), MaxRetries: new(0), Fetch: clsClient(func(r *http.Request) (*http.Response, error) {
			<-r.Context().Done()
			return nil, r.Context().Err()
		})})

		if result.StopReason != "error" || result.ErrorMessage != "Request timed out after 5ms" {
			t.Errorf("result=%+v", result)
		}
	})
	// .upstream/v0.99.1/packages/ai/test/typesafe-system-one.test.ts:188
	t.Run("creates a fresh timeout for every retry attempt", func(t *testing.T) {
		var mu sync.Mutex
		var signals []context.Context
		attempt := 0
		result := ClassifyTypesafeSystemOne(t.Context(), typesafeTestModel(), typesafeTestContext(), ClassifierOptions{APIKey: "secret", TimeoutMs: new(1000), MaxRetries: new(1), Fetch: clsClient(func(r *http.Request) (*http.Response, error) {
			mu.Lock()
			defer mu.Unlock()
			if _, ok := r.Context().Deadline(); !ok {
				t.Error("missing request signal")
			}
			signals = append(signals, r.Context())
			attempt++
			if attempt == 1 {
				return clsText(500, "retry", map[string]string{"retry-after-ms": "0"}), nil
			}
			return clsJSON(200, `{"answers":`+typesafeWireAnswers+`}`), nil
		})})

		if result.StopReason != "stop" || len(signals) != 2 || signals[0] == signals[1] {
			t.Errorf("result=%+v signals=%d", result, len(signals))
		}
	})
	// .upstream/v0.99.1/packages/ai/test/typesafe-system-one.test.ts:210
	t.Run("returns malformed responses as classifier errors", func(t *testing.T) {
		result := ClassifyTypesafeSystemOne(t.Context(), typesafeTestModel(), typesafeTestContext(), ClassifierOptions{APIKey: "secret", Fetch: clsClient(func(*http.Request) (*http.Response, error) {
			return clsJSON(200, `{"answers":{},"usage":{"input_tokens":10,"output_tokens":2}}`), nil
		})})

		if result.StopReason != "error" || len(result.Answers) != 0 || !strings.Contains(result.ErrorMessage, "did not return an answer for category") {
			t.Errorf("result=%+v", result)
		}
		// The request was billed, so its usage is kept.
		if result.Usage == nil || result.Usage.Input != 10 || result.Usage.Output != 2 {
			t.Errorf("usage=%+v", result.Usage)
		}
	})
	// .upstream/v0.99.1/packages/ai/test/typesafe-system-one.test.ts:223
	t.Run("ignores malformed usage", func(t *testing.T) {
		result := ClassifyTypesafeSystemOne(t.Context(), typesafeTestModel(), typesafeTestContext(), ClassifierOptions{APIKey: "secret", Fetch: clsClient(func(*http.Request) (*http.Response, error) {
			return clsJSON(200, `{"answers":`+typesafeWireAnswers+`,"usage":{"input_tokens":"many","output_tokens":3}}`), nil
		})})
		withoutTokens := ClassifyTypesafeSystemOne(t.Context(), typesafeTestModel(), typesafeTestContext(), ClassifierOptions{APIKey: "secret", Fetch: clsClient(func(*http.Request) (*http.Response, error) {
			return clsJSON(200, `{"answers":`+typesafeWireAnswers+`,"usage":{"cost":0.1}}`), nil
		})})

		if result.StopReason != "stop" || result.Usage == nil || result.Usage.Input != 0 || result.Usage.Output != 3 || result.Usage.TotalTokens != 3 {
			t.Errorf("result=%+v usage=%+v", result, result.Usage)
		}
		if withoutTokens.StopReason != "stop" || withoutTokens.Usage != nil {
			t.Errorf("withoutTokens=%+v", withoutTokens)
		}
	})
}
