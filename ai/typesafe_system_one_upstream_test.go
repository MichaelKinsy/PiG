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
	// .upstream/v1.3.0+7f225f25/packages/ai/test/typesafe-system-one.test.ts:123
	t.Run("rejects image input for text-only models before sending", func(t *testing.T) {
		calls := 0
		fetch := clsClient(func(*http.Request) (*http.Response, error) {
			calls++
			return clsJSON(200, `{"answers":`+typesafeWireAnswers+`}`), nil
		})
		model := typesafeTestModel()
		request := typesafeTestContext()
		request.Images = []ImageContent{{Data: "aW1hZ2U=", MimeType: "image/png"}}

		result := ClassifyTypesafeSystemOne(t.Context(), model, request, ClassifierOptions{APIKey: "secret", Fetch: fetch})
		cloudflareModel := model
		cloudflareModel.API = ClassifierAPICloudflareWorkersAISystemOne
		cloudflare := ClassifyCloudflareWorkersAISystemOne(t.Context(), cloudflareModel, request, ClassifierOptions{APIKey: "secret", Fetch: fetch})

		if calls != 0 || result.StopReason != "error" || result.ErrorMessage != "System One API does not support image input for jev-latest" {
			t.Errorf("calls=%d result=%+v", calls, result)
		}
		// The shared System One path rejects images for every System One service.
		if cloudflare.ErrorMessage != "Cloudflare Workers AI does not support image input for jev-latest" {
			t.Errorf("cloudflare=%+v", cloudflare)
		}
	})
	type recordedRequest struct {
		body    map[string]any
		headers http.Header
	}
	recordingFetch := func(t *testing.T, requests *[]recordedRequest) *http.Client {
		return clsClient(func(r *http.Request) (*http.Response, error) {
			*requests = append(*requests, recordedRequest{body: clsBody(t, r), headers: r.Header.Clone()})
			return clsJSON(200, `{"answers":`+typesafeWireAnswers+`}`), nil
		})
	}
	pngImage := ImageContent{Data: "cG5n", MimeType: "image/png"}
	imageURLState := func(state any) []any {
		return []any{map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,cG5n"}}, state}
	}
	// .upstream/v1.3.0+7f225f25/packages/ai/test/typesafe-system-one.test.ts:136
	t.Run("sends images to TypeSafe as assets with the opt-in header", func(t *testing.T) {
		var requests []recordedRequest
		fetch := recordingFetch(t, &requests)
		imageModel := typesafeTestModel()
		imageModel.Input = []string{"text", "image"}
		request := typesafeTestContext()
		withImages := request
		withImages.Images = []ImageContent{pngImage, {Data: "anBlZw==", MimeType: "image/jpeg"}}

		result := ClassifyTypesafeSystemOne(t.Context(), imageModel, withImages, ClassifierOptions{APIKey: "secret", Fetch: fetch})
		without := ClassifyTypesafeSystemOne(t.Context(), imageModel, request, ClassifierOptions{APIKey: "secret", Fetch: fetch})

		if result.StopReason != "stop" || without.StopReason != "stop" || len(requests) != 2 {
			t.Fatalf("result=%+v without=%+v requests=%d", result, without, len(requests))
		}
		wantState := []any{"<image-1>", "<image-2>", map[string]any{"text": "The deployment succeeded, thank you."}}
		wantAssets := map[string]any{
			"<image-1>": map[string]any{"type": "image", "format": "png", "data_base64": "cG5n"},
			"<image-2>": map[string]any{"type": "image", "format": "jpeg", "data_base64": "anBlZw=="},
		}
		if !reflect.DeepEqual(requests[0].body["state"], wantState) || !reflect.DeepEqual(requests[0].body["assets"], wantAssets) {
			t.Errorf("body=%v", requests[0].body)
		}
		if requests[0].headers.Get("typesafe-danger-zone-images") != "true" {
			t.Errorf("headers=%v", requests[0].headers)
		}
		// Requests without images keep the plain state and send no opt-in header.
		if !reflect.DeepEqual(requests[1].body["state"], map[string]any{"text": "The deployment succeeded, thank you."}) || requests[1].body["assets"] != nil || requests[1].headers.Get("typesafe-danger-zone-images") != "" {
			t.Errorf("without images: %v %v", requests[1].body, requests[1].headers)
		}
	})
	// .upstream/v1.3.0+7f225f25/packages/ai/test/typesafe-system-one.test.ts:179
	t.Run("rejects image formats TypeSafe does not accept before sending", func(t *testing.T) {
		calls := 0
		fetch := clsClient(func(*http.Request) (*http.Response, error) {
			calls++
			return clsJSON(200, `{"answers":`+typesafeWireAnswers+`}`), nil
		})
		model := typesafeTestModel()
		model.Input = []string{"text", "image"}
		request := typesafeTestContext()
		request.Images = []ImageContent{{Data: "d2VicA==", MimeType: "image/webp"}}

		result := ClassifyTypesafeSystemOne(t.Context(), model, request, ClassifierOptions{APIKey: "secret", Fetch: fetch})

		if calls != 0 || result.StopReason != "error" || result.ErrorMessage != "System One API accepts PNG and JPEG images for jev-latest, got image/webp" {
			t.Errorf("calls=%d result=%+v", calls, result)
		}
	})
	// .upstream/v1.3.0+7f225f25/packages/ai/test/typesafe-system-one.test.ts:192
	t.Run("sends images to OpenRouter as image_url parts in the state", func(t *testing.T) {
		var requests []recordedRequest
		model := typesafeTestModel()
		model.ID, model.Provider, model.BaseURL, model.Input = "typesafe/jev-1.14", "openrouter", "https://openrouter.ai/api/v1", []string{"text", "image"}
		request := typesafeTestContext()
		request.Images = []ImageContent{pngImage}

		result := ClassifyTypesafeSystemOne(t.Context(), model, request, ClassifierOptions{APIKey: "secret", Fetch: recordingFetch(t, &requests)})

		if result.StopReason != "stop" || len(requests) != 1 {
			t.Fatalf("result=%+v requests=%d", result, len(requests))
		}
		// OpenRouter answers 200 for assets but drops them, and rejects a top-level images list.
		body := requests[0].body
		if body["model"] != "typesafe/jev-1.14" || !reflect.DeepEqual(body["state"], imageURLState(map[string]any{"text": "The deployment succeeded, thank you."})) || body["questions"] == nil || len(body) != 3 {
			t.Errorf("body=%v", body)
		}
		if requests[0].headers.Get("typesafe-danger-zone-images") != "" {
			t.Errorf("headers=%v", requests[0].headers)
		}
	})
	// .upstream/v1.3.0+7f225f25/packages/ai/test/typesafe-system-one.test.ts:225
	for _, tc := range []struct {
		name     string
		provider string
		baseURL  string
		compat   *SystemOneCompat
	}{
		{"OpenRouter behind a proxy", "openrouter", "https://proxy.example.com/v1", nil},
		{"a regional OpenRouter endpoint", "custom", "https://us.openrouter.ai/api/v1", nil},
		{"a proxy declared through compat", "custom", "https://proxy.example.com/v1", &SystemOneCompat{ImageInput: SystemOneImageInputStateImageURLs, SendImageOptInHeader: new(false)}},
	} {
		t.Run("sends OpenRouter's image form to "+tc.name, func(t *testing.T) {
			var requests []recordedRequest
			model := typesafeTestModel()
			model.ID, model.Provider, model.BaseURL, model.Input, model.Compat = "typesafe/jev-1.14", tc.provider, tc.baseURL, []string{"text", "image"}, tc.compat
			request := typesafeTestContext()
			request.Images = []ImageContent{pngImage}

			result := ClassifyTypesafeSystemOne(t.Context(), model, request, ClassifierOptions{APIKey: "secret", Fetch: recordingFetch(t, &requests)})

			if result.StopReason != "stop" || len(requests) != 1 {
				t.Fatalf("result=%+v requests=%d", result, len(requests))
			}
			if !reflect.DeepEqual(requests[0].body["state"], imageURLState(map[string]any{"text": "The deployment succeeded, thank you."})) || requests[0].body["assets"] != nil || requests[0].headers.Get("typesafe-danger-zone-images") != "" {
				t.Errorf("body=%v headers=%v", requests[0].body, requests[0].headers)
			}
		})
	}
	// .upstream/v1.3.0+7f225f25/packages/ai/test/typesafe-system-one.test.ts:262
	t.Run("lets compat override detected image settings per field", func(t *testing.T) {
		var requests []recordedRequest
		model := typesafeTestModel()
		model.Input, model.Compat = []string{"text", "image"}, &SystemOneCompat{SendImageOptInHeader: new(false)}
		request := typesafeTestContext()
		request.Images = []ImageContent{pngImage}

		ClassifyTypesafeSystemOne(t.Context(), model, request, ClassifierOptions{APIKey: "secret", Fetch: recordingFetch(t, &requests)})

		// Only the header is overridden; the detected assets form stays.
		if len(requests) != 1 || requests[0].body["assets"] == nil || requests[0].headers.Get("typesafe-danger-zone-images") != "" {
			t.Errorf("requests=%v", requests)
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
