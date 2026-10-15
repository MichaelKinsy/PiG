package ai

import (
	"encoding/json"
	"math"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func openAIDecisionsTestModel() ClassifierModel {
	return ClassifierModel{ID: "gpt-6-luna", Name: "GPT-6 Luna", API: "openai-decisions", Provider: "openai", BaseURL: "https://api.openai.com/v1", Input: []string{"text", "image"},
		Cost:          ModelCost{Input: 0.1, Tiers: []CostTier{{InputTokensAbove: 272000, InputCostPer1M: 0.2}}},
		ContextWindow: 922000}
}

func openAIDecisionsTestContext() ClassifierContext {
	return ClassifierContext{
		State: JsonObject{"text": "The deployment succeeded, thank you."},
		Questions: ClassifierQuestions{
			{ID: "category", Question: ClassifierChoiceQuestion{Instructions: "Classify the message", Criteria: clsChoices("success", "Successful", "failure", "")}},
			{ID: "satisfaction", Question: ClassifierScoreQuestion{Instructions: "Score satisfaction", Criteria: []string{"low", "neutral", "high"}}},
			{ID: "approved", Question: ClassifierBoolQuestion{Instructions: "Does the user approve?", Criteria: ClassifierBoolCriteria{True: "Approval", False: "No approval"}}},
		},
	}
}

// The response shape from the API reference and live gpt-6-luna requests (openai-decisions.test.ts wireAnswers).
var openAIDecisionsWireAnswers = []string{
	`{"type":"choice","name":"category","choice":"success","probabilities":[{"value":"success","probability":0.9},{"value":"failure","probability":0.1}],"confidence":0.8}`,
	`{"type":"score","name":"satisfaction","score":1.8,"probabilities":[{"value":0,"label":"low","probability":0.05},{"value":1,"label":"neutral","probability":0.1},{"value":2,"label":"high","probability":0.85}],"confidence":0.7}`,
	`{"type":"predicate","name":"approved","probability":0.95}`,
}

func openAIDecisionsAnswersJSON(answers ...string) string {
	return "[" + strings.Join(answers, ",") + "]"
}

const openAIDecisionsWireUsage = `{"input_tokens":164,"input_tokens_details":{"cached_tokens":0,"cache_write_tokens":0},"output_tokens":0,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":164}`

var openAIDecisionsTestImage = ImageContent{Data: "aW1hZ2U=", MimeType: "image/png"}

func openAIDecisionsOptions(fetch clsFetch) ClassifierOptions {
	return ClassifierOptions{APIKey: "secret", Fetch: clsClient(fetch)}
}

// Ports packages/ai/test/openai-decisions.test.ts.
func TestOpenAIDecisionsUpstream(t *testing.T) {
	model, request := openAIDecisionsTestModel(), openAIDecisionsTestContext()
	all := openAIDecisionsAnswersJSON(openAIDecisionsWireAnswers...)

	// openai-decisions.test.ts:70 maps questions to Decisions types and answers back by name
	t.Run("maps questions to Decisions types and answers back by name", func(t *testing.T) {
		calls := 0
		var requestBody map[string]any
		fetch := func(r *http.Request) (*http.Response, error) {
			calls++
			if r.URL.String() != "https://api.openai.com/v1/decisions" || r.Header.Get("authorization") != "Bearer secret" {
				t.Errorf("url=%s authorization=%q", r.URL, r.Header.Get("authorization"))
			}
			raw, _ := json.Marshal(clsBody(t, r))
			_ = json.Unmarshal(raw, &requestBody)
			// Answers out of question order: they are matched by name.
			return clsJSON(200, `{"model":"gpt-6-luna","answers":`+openAIDecisionsAnswersJSON(openAIDecisionsWireAnswers[2], openAIDecisionsWireAnswers[1], openAIDecisionsWireAnswers[0])+`,"usage":`+openAIDecisionsWireUsage+`}`), nil
		}

		result := ClassifyOpenAIDecisions(t.Context(), model, request, ClassifierOptions{APIKey: "secret", Fetch: clsClient(fetch), Temperature: new(1.5)})

		if calls != 1 {
			t.Fatalf("calls=%d", calls)
		}
		want := map[string]any{
			"model": "gpt-6-luna",
			"input": `{"text":"The deployment succeeded, thank you."}`,
			"questions": []any{
				// Empty descriptions are omitted.
				map[string]any{"type": "choice", "name": "category", "instructions": "Classify the message", "choices": []any{map[string]any{"value": "success", "description": "Successful"}, map[string]any{"value": "failure"}}},
				map[string]any{"type": "score", "name": "satisfaction", "instructions": "Score satisfaction", "levels": []any{map[string]any{"label": "low"}, map[string]any{"label": "neutral"}, map[string]any{"label": "high"}}},
				map[string]any{"type": "predicate", "name": "approved", "instructions": "Does the user approve?\n\nTrue means: Approval\nFalse means: No approval"},
			},
		}
		if !reflect.DeepEqual(requestBody, want) {
			t.Fatalf("request body=%v\nwant %v", requestBody, want)
		}
		if result.StopReason != "stop" {
			t.Fatalf("result=%+v", result)
		}
		if got := clsAnswerIDs(result.Answers); !reflect.DeepEqual(got, []string{"category", "satisfaction", "approved"}) {
			t.Errorf("answer order=%v", got)
		}
		wantCategory := ClassifierChoiceAnswer{Choice: "success", Probabilities: []ClassifierProbability{{"success", 0.9}, {"failure", 0.1}}, Confidence: 0.8}
		if got := clsAnswer(t, result.Answers, "category"); !reflect.DeepEqual(got, wantCategory) {
			t.Errorf("category=%+v", got)
		}
		if got := clsAnswer(t, result.Answers, "satisfaction"); got != (ClassifierScoreAnswer{Score: 1.8, Confidence: 0.7}) {
			t.Errorf("satisfaction=%+v", got)
		}
		if got := clsAnswer(t, result.Answers, "approved"); got != (ClassifierBoolAnswer{Probability: 0.95}) {
			t.Errorf("approved=%+v", got)
		}
		if result.Usage == nil || result.Usage.Input != 164 || result.Usage.Output != 0 || result.Usage.CacheRead != 0 || result.Usage.TotalTokens != 164 || math.Abs(result.Usage.Cost.Total-0.0000164) > 5e-13 {
			t.Errorf("usage=%+v", result.Usage)
		}
	})

	// openai-decisions.test.ts:123
	t.Run("prices long-context requests at the long-context input rate", func(t *testing.T) {
		result := ClassifyOpenAIDecisions(t.Context(), model, request, openAIDecisionsOptions(func(*http.Request) (*http.Response, error) {
			return clsJSON(200, `{"answers":`+all+`,"usage":{"input_tokens":300000,"output_tokens":0}}`), nil
		}))

		if result.Usage == nil || math.Abs(result.Usage.Cost.Total-0.06) > 5e-13 {
			t.Fatalf("result=%+v", result)
		}
	})

	// openai-decisions.test.ts:132
	t.Run("sends images after the state in one user message", func(t *testing.T) {
		var input any
		jpeg := ImageContent{Data: "aW1hZ2U=", MimeType: "image/jpeg"}
		withImages := request
		withImages.Images = []ImageContent{openAIDecisionsTestImage, jpeg}

		result := ClassifyOpenAIDecisions(t.Context(), model, withImages, openAIDecisionsOptions(func(r *http.Request) (*http.Response, error) {
			input = clsBody(t, r)["input"]
			return clsJSON(200, `{"answers":`+all+`}`), nil
		}))

		if result.StopReason != "stop" {
			t.Fatalf("result=%+v", result)
		}
		want := []any{map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "input_text", "text": `{"text":"The deployment succeeded, thank you."}`},
			map[string]any{"type": "input_image", "image_url": "data:image/png;base64,aW1hZ2U="},
			map[string]any{"type": "input_image", "image_url": "data:image/jpeg;base64,aW1hZ2U="},
		}}}
		if !reflect.DeepEqual(input, want) {
			t.Fatalf("input=%v", input)
		}
	})

	// openai-decisions.test.ts:158
	t.Run("rejects more than 128 images before sending", func(t *testing.T) {
		calls := 0
		withImages := request
		for range 129 {
			withImages.Images = append(withImages.Images, openAIDecisionsTestImage)
		}

		result := ClassifyOpenAIDecisions(t.Context(), model, withImages, openAIDecisionsOptions(func(*http.Request) (*http.Response, error) {
			calls++
			return clsJSON(200, `{"answers":`+all+`}`), nil
		}))

		if calls != 0 || result.StopReason != "error" || !strings.Contains(result.ErrorMessage, "at most 128 images, got 129") {
			t.Fatalf("calls=%d result=%+v", calls, result)
		}
		// 128 images are accepted.
		withImages.Images = withImages.Images[:128]
		if accepted := ClassifyOpenAIDecisions(t.Context(), model, withImages, openAIDecisionsOptions(func(*http.Request) (*http.Response, error) {
			return clsJSON(200, `{"answers":`+all+`}`), nil
		})); accepted.StopReason != "stop" {
			t.Fatalf("128 images: %+v", accepted)
		}
	})

	// openai-decisions.test.ts:177
	t.Run("fails the result when a question is refused and keeps the billed usage", func(t *testing.T) {
		result := ClassifyOpenAIDecisions(t.Context(), model, request, openAIDecisionsOptions(func(*http.Request) (*http.Response, error) {
			return clsJSON(200, `{"answers":`+openAIDecisionsAnswersJSON(openAIDecisionsWireAnswers[0], openAIDecisionsWireAnswers[1], `{"type":"refusal","name":"approved"}`)+`,"usage":`+openAIDecisionsWireUsage+`}`), nil
		}))

		if result.StopReason != "error" || len(result.Answers) != 0 || result.ErrorMessage != "OpenAI Decisions refused to answer approved" {
			t.Fatalf("result=%+v", result)
		}
		if result.Usage == nil || result.Usage.Input != 164 {
			t.Fatalf("usage=%+v", result.Usage)
		}
	})

	// openai-decisions.test.ts:195
	t.Run("returns missing and mistyped answers as classifier errors", func(t *testing.T) {
		missing := ClassifyOpenAIDecisions(t.Context(), model, request, openAIDecisionsOptions(func(*http.Request) (*http.Response, error) {
			return clsJSON(200, `{"answers":`+openAIDecisionsAnswersJSON(openAIDecisionsWireAnswers[:2]...)+`}`), nil
		}))
		mistyped := ClassifyOpenAIDecisions(t.Context(), model, request, openAIDecisionsOptions(func(*http.Request) (*http.Response, error) {
			return clsJSON(200, `{"answers":`+openAIDecisionsAnswersJSON(openAIDecisionsWireAnswers[0], openAIDecisionsWireAnswers[1], `{"type":"score","name":"approved"}`)+`}`), nil
		}))

		if missing.StopReason != "error" || !strings.Contains(missing.ErrorMessage, "did not return an answer for approved") {
			t.Errorf("missing=%+v", missing)
		}
		if mistyped.StopReason != "error" || !strings.Contains(mistyped.ErrorMessage, "did not return a predicate answer for approved") {
			t.Errorf("mistyped=%+v", mistyped)
		}
	})

	// openai-decisions.test.ts:214
	t.Run("preserves prototype-sensitive question IDs in answers", func(t *testing.T) {
		var questions ClassifierQuestions
		if err := json.Unmarshal([]byte(`{"__proto__":{"type":"bool","instructions":"Is this true?","criteria":{"true":"Yes","false":"No"}}}`), &questions); err != nil {
			t.Fatal(err)
		}

		result := ClassifyOpenAIDecisions(t.Context(), model, ClassifierContext{State: JsonObject{}, Questions: questions}, openAIDecisionsOptions(func(*http.Request) (*http.Response, error) {
			return clsJSON(200, `{"answers":[{"type":"predicate","name":"__proto__","probability":0.75}]}`), nil
		}))

		if result.StopReason != "stop" || !reflect.DeepEqual(clsAnswerIDs(result.Answers), []string{"__proto__"}) || clsAnswer(t, result.Answers, "__proto__") != (ClassifierBoolAnswer{Probability: 0.75}) {
			t.Fatalf("result=%+v", result)
		}
	})

	// openai-decisions.test.ts:232
	t.Run("does not retry gateway timeouts and explains them instead of returning the HTML page", func(t *testing.T) {
		calls := 0
		// Default retries: the same input would time out again.
		result := ClassifyOpenAIDecisions(t.Context(), model, request, openAIDecisionsOptions(func(*http.Request) (*http.Response, error) {
			calls++
			return clsText(504, "<!DOCTYPE html><html>Gateway time-out</html>", map[string]string{"retry-after-ms": "0"}), nil
		}))

		if calls != 1 || result.StopReason != "error" {
			t.Fatalf("calls=%d result=%+v", calls, result)
		}
		if !strings.Contains(result.ErrorMessage, "OpenAI Decisions error (504): the request timed out at the gateway") || strings.Contains(result.ErrorMessage, "<html>") {
			t.Fatalf("errorMessage=%q", result.ErrorMessage)
		}
	})

	// openai-decisions.test.ts:249
	t.Run("still retries other server errors", func(t *testing.T) {
		attempt := 0
		result := ClassifyOpenAIDecisions(t.Context(), model, request, openAIDecisionsOptions(func(*http.Request) (*http.Response, error) {
			attempt++
			if attempt == 1 {
				return clsText(503, "busy", map[string]string{"retry-after-ms": "0"}), nil
			}
			return clsJSON(200, `{"answers":`+all+`}`), nil
		}))

		if attempt != 2 || result.StopReason != "stop" {
			t.Fatalf("attempt=%d result=%+v", attempt, result)
		}
	})

	// openai-decisions.test.ts:263
	t.Run("includes the API error body for other HTTP failures", func(t *testing.T) {
		result := ClassifyOpenAIDecisions(t.Context(), model, request, ClassifierOptions{APIKey: "secret", MaxRetries: new(0), Fetch: clsClient(func(*http.Request) (*http.Response, error) {
			return clsJSON(400, `{"error":{"message":"Decision input exceeds the token limit.","type":"invalid_request_error"}}`), nil
		})})

		if result.StopReason != "error" || !strings.Contains(result.ErrorMessage, "OpenAI Decisions error (400)") || !strings.Contains(result.ErrorMessage, "Decision input exceeds the token limit.") {
			t.Fatalf("result=%+v", result)
		}
	})

	// openai-decisions.test.ts:281
	t.Run("rejects models for other classifier APIs and missing API keys", func(t *testing.T) {
		calls := 0
		fetch := clsClient(func(*http.Request) (*http.Response, error) {
			calls++
			return clsJSON(200, `{"answers":`+all+`}`), nil
		})
		other := model
		other.API = "typesafe-system-one"

		otherAPI := ClassifyOpenAIDecisions(t.Context(), other, request, ClassifierOptions{APIKey: "secret", Fetch: fetch})
		noKey := ClassifyOpenAIDecisions(t.Context(), model, request, ClassifierOptions{Fetch: fetch})

		if calls != 0 {
			t.Fatalf("calls=%d", calls)
		}
		if !strings.Contains(otherAPI.ErrorMessage, "Unsupported classifier API: typesafe-system-one") {
			t.Errorf("otherAPI=%+v", otherAPI)
		}
		if !strings.Contains(noKey.ErrorMessage, "No API key for provider: openai") {
			t.Errorf("noKey=%+v", noKey)
		}
	})
}

// Ports the noRetryStatuses case of packages/ai/test/provider-retry.test.ts (:40) against the classifier retry loop,
// which implements retryProviderRequest for the classifier APIs.
func TestRetryClassifierRequestNoRetryStatuses(t *testing.T) {
	failure := func() *ClassifierHTTPError {
		return classifierHTTPError("Provider", 504, http.Header{"Retry-After-Ms": {"0"}}, "")
	}
	maxRetries := 2
	options := ClassifierOptions{MaxRetries: &maxRetries}

	calls := 0
	_, err := retryClassifierRequest(t.Context(), options, func() (string, error) { calls++; return "", failure() }, 504)
	if calls != 1 || err == nil {
		t.Fatalf("listed status: calls=%d err=%v", calls, err)
	}
	if got, ok := err.(*ClassifierHTTPError); !ok || got.Status == nil || *got.Status != 504 {
		t.Fatalf("err=%v is not the original error", err)
	}

	// Without the list the same status is retried to the budget.
	calls = 0
	_, _ = retryClassifierRequest(t.Context(), options, func() (string, error) { calls++; return "", failure() })
	if calls != 3 {
		t.Fatalf("unlisted status: calls=%d, want 3", calls)
	}
	// Another status stays retryable when only 504 is listed.
	calls = 0
	_, _ = retryClassifierRequest(t.Context(), options, func() (string, error) {
		calls++
		return "", classifierHTTPError("Provider", 503, http.Header{"Retry-After-Ms": {"0"}}, "")
	}, 504)
	if calls != 3 {
		t.Fatalf("other status: calls=%d, want 3", calls)
	}
}
