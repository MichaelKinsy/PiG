package ai

import (
	"math"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

const cloudflareJevOutput = `{"model":"jev-1.13.0","answers":{"is_urgent":{"type":"noul","noul":0.95},"department":{"type":"choice","choice":"billing","confidence":0.8,"probabilities":{"billing":0.87,"technical":0.13}}},"usage":{"input_tokens":426,"output_tokens":73}}`

func cloudflareTestContext() ClassifierContext {
	return ClassifierContext{State: JsonObject{"message": "Help! My payouts have been failing for 3 days."}, Questions: ClassifierQuestions{
		{ID: "is_urgent", Question: ClassifierBoolQuestion{Instructions: "Does this convey urgency?", Criteria: ClassifierBoolCriteria{True: "Explicitly time-sensitive", False: "No urgency expressed"}}},
		{ID: "department", Question: ClassifierChoiceQuestion{Instructions: "Which team should handle this?", Criteria: clsChoices("billing", "Payments", "technical", "Bugs")}},
	}}
}

// REST envelope observed from the live /ai/run endpoint.
func cloudflareRESTResponse(state, result string) string {
	return `{"result":{"state":"` + state + `","result":` + result + `,"gatewayMetadata":{"keySource":"Unified"}},"success":true,"errors":[],"messages":[]}`
}

func cloudflareClassifierSetup(t *testing.T) (*Models, *ClassifierModel) {
	t.Helper()
	models := CreateModels()
	models.SetProvider(CloudflareWorkersAIProvider())
	jev, _ := models.GetModelOfType(ModelTypeClassifier, "cloudflare-workers-ai", "typesafe/jev").(*ClassifierModel)
	if jev == nil {
		t.Fatal("missing Cloudflare Jev model")
	}
	return models, jev
}

func cloudflareClassifierOptions(fetch clsFetch) ModelsClassifierOptions {
	return ModelsClassifierOptions{ClassifierOptions: ClassifierOptions{APIKey: "cf-key", APIKeySet: true, Env: ProviderEnv{"CLOUDFLARE_ACCOUNT_ID": "account-id"}, Fetch: clsClient(fetch)}}
}

// Ports packages/ai/test/cloudflare-workers-ai-system-one.test.ts.
func TestCloudflareWorkersAISystemOneUpstream(t *testing.T) {
	// .upstream/v0.99.1/packages/ai/test/cloudflare-workers-ai-system-one.test.ts:59
	t.Run("exposes Jev only through classifier catalog accessors", func(t *testing.T) {
		models, jev := cloudflareClassifierSetup(t)
		if !reflect.DeepEqual(jev, GetBuiltinClassifierModel("cloudflare-workers-ai", "typesafe/jev")) {
			t.Error("provider model differs from the catalog model")
		}
		if jev.ModelType() != ModelTypeClassifier || jev.API != "cloudflare-workers-ai-system-one" {
			t.Errorf("jev=%+v", jev)
		}
		if models.GetModel("cloudflare-workers-ai", "typesafe/jev") != nil {
			t.Error("chat lookup returned Jev")
		}
	})
	// .upstream/v0.99.1/packages/ai/test/cloudflare-workers-ai-system-one.test.ts:66
	t.Run("runs Jev through the account-scoped /ai/run endpoint", func(t *testing.T) {
		models, jev := cloudflareClassifierSetup(t)
		var requested string
		result := models.Classify(t.Context(), jev, cloudflareTestContext(), cloudflareClassifierOptions(func(r *http.Request) (*http.Response, error) {
			requested = r.URL.String()
			payload := clsBody(t, r)
			input, _ := payload["input"].(map[string]any)
			questions, _ := input["questions"].(map[string]any)
			if payload["model"] != "typesafe/jev" || !reflect.DeepEqual(input["state"], map[string]any{"message": "Help! My payouts have been failing for 3 days."}) ||
				questions["is_urgent"].(map[string]any)["type"] != "noul" || questions["department"].(map[string]any)["type"] != "choice" {
				t.Errorf("payload=%v", payload)
			}
			if r.Header.Get("authorization") != "Bearer cf-key" {
				t.Errorf("authorization=%q", r.Header.Get("authorization"))
			}
			return clsJSON(200, cloudflareRESTResponse("Completed", cloudflareJevOutput)), nil
		}))

		if requested != "https://api.cloudflare.com/client/v4/accounts/account-id/ai/run" {
			t.Errorf("url=%s", requested)
		}
		if result.StopReason != "stop" || clsAnswer(t, result.Answers, "is_urgent") != (ClassifierBoolAnswer{Probability: 0.95}) {
			t.Fatalf("result=%+v", result)
		}
		if department, ok := clsAnswer(t, result.Answers, "department").(ClassifierChoiceAnswer); !ok || department.Choice != "billing" || department.Confidence != 0.8 {
			t.Errorf("department=%+v", department)
		}
		if result.Usage == nil || result.Usage.Input != 426 || result.Usage.Output != 73 || result.Usage.TotalTokens != 499 {
			t.Errorf("usage=%+v", result.Usage)
		}
	})
	// .upstream/v1.0.1/packages/ai/test/cloudflare-workers-ai-system-one.test.ts:106 (#10316, #10322)
	for _, tc := range []struct {
		id         string
		inputPrice float64
	}{{"@cf/cloudflare/clef", 0.24}, {"@cf/cloudflare/clef-flash", 0.038}, {"@cf/cloudflare/clef-omni", 0.15}} {
		t.Run("runs "+tc.id+" through /ai/run and parses its direct output", func(t *testing.T) {
			models, _ := cloudflareClassifierSetup(t)
			clef, _ := models.GetModelOfType(ModelTypeClassifier, "cloudflare-workers-ai", tc.id).(*ClassifierModel)
			if clef == nil {
				t.Fatalf("missing Cloudflare %s model", tc.id)
			}
			var requested string
			result := models.Classify(t.Context(), clef, cloudflareTestContext(), cloudflareClassifierOptions(func(r *http.Request) (*http.Response, error) {
				requested = r.URL.String()
				payload := clsBody(t, r)
				input, _ := payload["input"].(map[string]any)
				questions, _ := input["questions"].(map[string]any)
				if payload["model"] != tc.id || !reflect.DeepEqual(input["state"], map[string]any{"message": "Help! My payouts have been failing for 3 days."}) ||
					questions["is_urgent"].(map[string]any)["type"] != "noul" {
					t.Errorf("payload=%v", payload)
				}
				// Cloudflare-hosted output observed from a live /ai/run call: the envelope carries the output directly, without a run record.
				return clsJSON(200, `{"result":{"model":"clef","answers":{"is_urgent":{"type":"noul","noul":0.9912},"department":{"type":"choice","choice":"technical","probabilities":{"billing":0.1632,"technical":0.8368},"confidence":0.4538}},"usage":{"input_tokens":222,"output_tokens":0}},"success":true,"errors":[],"messages":[]}`), nil
			}))

			if requested != "https://api.cloudflare.com/client/v4/accounts/account-id/ai/run" {
				t.Errorf("url=%s", requested)
			}
			if result.StopReason != "stop" || clsAnswer(t, result.Answers, "is_urgent") != (ClassifierBoolAnswer{Probability: 0.9912}) {
				t.Fatalf("result=%+v", result)
			}
			if department, ok := clsAnswer(t, result.Answers, "department").(ClassifierChoiceAnswer); !ok || department.Choice != "technical" || department.Confidence != 0.4538 {
				t.Errorf("department=%+v", department)
			}
			if result.Usage == nil || result.Usage.Input != 222 || result.Usage.Output != 0 || result.Usage.TotalTokens != 222 {
				t.Fatalf("usage=%+v", result.Usage)
			}
			if want := 222 * tc.inputPrice / 1_000_000; math.Abs(result.Usage.Cost.Input-want) > 5e-3 {
				t.Errorf("input cost=%v, want %v", result.Usage.Cost.Input, want)
			}
		})
	}
	cloudflareClefOutput := `{"result":{"model":"clef","answers":{"is_urgent":{"type":"noul","noul":0.9912},"department":{"type":"choice","choice":"technical","probabilities":{"billing":0.1632,"technical":0.8368},"confidence":0.4538}},"usage":{"input_tokens":222,"output_tokens":0}},"success":true,"errors":[],"messages":[]}`
	cloudflareImage := ImageContent{Data: "cG5n", MimeType: "image/png"}
	// .upstream/v1.3.0+7f225f25/packages/ai/test/cloudflare-workers-ai-system-one.test.ts:135
	t.Run("sends images to Clef as data URLs", func(t *testing.T) {
		models, _ := cloudflareClassifierSetup(t)
		clef, _ := models.GetModelOfType(ModelTypeClassifier, "cloudflare-workers-ai", "@cf/cloudflare/clef").(*ClassifierModel)
		if clef == nil {
			t.Fatal("missing Cloudflare Clef model")
		}
		var inputs []map[string]any
		var headers []http.Header
		request := cloudflareTestContext()
		request.Images = []ImageContent{cloudflareImage, {Data: "d2VicA==", MimeType: "image/webp"}}

		result := models.Classify(t.Context(), clef, request, cloudflareClassifierOptions(func(r *http.Request) (*http.Response, error) {
			input, _ := clsBody(t, r)["input"].(map[string]any)
			inputs, headers = append(inputs, input), append(headers, r.Header.Clone())
			return clsJSON(200, cloudflareClefOutput), nil
		}))

		if result.StopReason != "stop" || len(inputs) != 1 {
			t.Fatalf("result=%+v inputs=%d", result, len(inputs))
		}
		// Clef rejects assets with a 422, so the state stays as is.
		if !reflect.DeepEqual(inputs[0]["state"], map[string]any{"message": "Help! My payouts have been failing for 3 days."}) || inputs[0]["assets"] != nil ||
			!reflect.DeepEqual(inputs[0]["images"], []any{"data:image/png;base64,cG5n", "data:image/webp;base64,d2VicA=="}) || inputs[0]["questions"] == nil || len(inputs[0]) != 3 {
			t.Errorf("input=%v", inputs[0])
		}
		if headers[0].Get("typesafe-danger-zone-images") != "" {
			t.Errorf("headers=%v", headers[0])
		}
	})
	// .upstream/v1.3.0+7f225f25/packages/ai/test/cloudflare-workers-ai-system-one.test.ts:166
	t.Run("reads the image form from compat before the model ID", func(t *testing.T) {
		models, jev := cloudflareClassifierSetup(t)
		clef, _ := models.GetModelOfType(ModelTypeClassifier, "cloudflare-workers-ai", "@cf/cloudflare/clef").(*ClassifierModel)
		if clef == nil || clef.Compat == nil || !reflect.DeepEqual(*clef.Compat, SystemOneCompat{ImageInput: SystemOneImageInputImages}) {
			t.Errorf("clef compat=%+v", clef)
		}
		if jev.Compat == nil || !reflect.DeepEqual(*jev.Compat, SystemOneCompat{ImageInput: SystemOneImageInputAssets}) {
			t.Errorf("jev compat=%+v", jev.Compat)
		}
		var inputs []map[string]any
		request := cloudflareTestContext()
		request.Images = []ImageContent{cloudflareImage}
		// A Clef deployment under an ID the prefix check does not recognize.
		finetune := *jev
		finetune.ID, finetune.Input, finetune.Compat = "@cf/acme/clef-finetune", []string{"text", "image"}, &SystemOneCompat{ImageInput: SystemOneImageInputImages}

		models.Classify(t.Context(), &finetune, request, cloudflareClassifierOptions(func(r *http.Request) (*http.Response, error) {
			input, _ := clsBody(t, r)["input"].(map[string]any)
			inputs = append(inputs, input)
			return clsJSON(200, cloudflareClefOutput), nil
		}))

		if len(inputs) != 1 || !reflect.DeepEqual(inputs[0]["images"], []any{"data:image/png;base64,cG5n"}) || inputs[0]["assets"] != nil {
			t.Errorf("inputs=%v", inputs)
		}
	})
	// .upstream/v1.3.0+7f225f25/packages/ai/test/cloudflare-workers-ai-system-one.test.ts:190
	t.Run("rejects more than four images for Clef before sending", func(t *testing.T) {
		models, _ := cloudflareClassifierSetup(t)
		clef, _ := models.GetModelOfType(ModelTypeClassifier, "cloudflare-workers-ai", "@cf/cloudflare/clef-flash").(*ClassifierModel)
		if clef == nil {
			t.Fatal("missing Cloudflare Clef Flash model")
		}
		calls := 0
		request := cloudflareTestContext()
		request.Images = []ImageContent{cloudflareImage, cloudflareImage, cloudflareImage, cloudflareImage, cloudflareImage}

		result := models.Classify(t.Context(), clef, request, cloudflareClassifierOptions(func(*http.Request) (*http.Response, error) {
			calls++
			return clsJSON(200, cloudflareClefOutput), nil
		}))

		if calls != 0 || result.StopReason != "error" || result.ErrorMessage != "Cloudflare Workers AI accepts at most 4 images for @cf/cloudflare/clef-flash, got 5" {
			t.Errorf("calls=%d result=%+v", calls, result)
		}
	})
	// .upstream/v1.3.0+7f225f25/packages/ai/test/cloudflare-workers-ai-system-one.test.ts:205
	for _, id := range []string{"typesafe/jev", "typesafe/jev-1.14.0"} {
		t.Run("sends images to "+id+" as System One assets", func(t *testing.T) {
			models, _ := cloudflareClassifierSetup(t)
			jev, _ := models.GetModelOfType(ModelTypeClassifier, "cloudflare-workers-ai", id).(*ClassifierModel)
			if jev == nil {
				t.Fatalf("missing Cloudflare %s model", id)
			}
			var bodies []map[string]any
			var headers []http.Header
			request := cloudflareTestContext()
			request.Images = []ImageContent{cloudflareImage}

			result := models.Classify(t.Context(), jev, request, cloudflareClassifierOptions(func(r *http.Request) (*http.Response, error) {
				bodies, headers = append(bodies, clsBody(t, r)), append(headers, r.Header.Clone())
				return clsJSON(200, cloudflareRESTResponse("Completed", cloudflareJevOutput)), nil
			}))

			if result.StopReason != "stop" || len(bodies) != 1 {
				t.Fatalf("result=%+v bodies=%d", result, len(bodies))
			}
			input, _ := bodies[0]["input"].(map[string]any)
			wantAssets := map[string]any{"<image-1>": map[string]any{"type": "image", "format": "png", "data_base64": "cG5n"}}
			if bodies[0]["model"] != id || !reflect.DeepEqual(input["state"], []any{"<image-1>", map[string]any{"message": "Help! My payouts have been failing for 3 days."}}) || !reflect.DeepEqual(input["assets"], wantAssets) {
				t.Errorf("body=%v", bodies[0])
			}
			// Workers AI needs no TypeSafe opt-in header.
			if headers[0].Get("typesafe-danger-zone-images") != "" {
				t.Errorf("headers=%v", headers[0])
			}
		})
	}
	// .upstream/v0.99.1/packages/ai/test/cloudflare-workers-ai-system-one.test.ts:90
	t.Run("reports runs that did not complete", func(t *testing.T) {
		models, jev := cloudflareClassifierSetup(t)
		result := models.Classify(t.Context(), jev, cloudflareTestContext(), cloudflareClassifierOptions(func(*http.Request) (*http.Response, error) {
			return clsJSON(200, cloudflareRESTResponse("Queued", "null")), nil
		}))

		if result.StopReason != "error" || !strings.Contains(result.ErrorMessage, "run did not complete (state: Queued)") {
			t.Errorf("result=%+v", result)
		}
	})
	// .upstream/v0.99.1/packages/ai/test/cloudflare-workers-ai-system-one.test.ts:101
	t.Run("reports Cloudflare envelope errors", func(t *testing.T) {
		models, jev := cloudflareClassifierSetup(t)
		result := models.Classify(t.Context(), jev, cloudflareTestContext(), cloudflareClassifierOptions(func(*http.Request) (*http.Response, error) {
			return clsJSON(200, `{"success":false,"errors":[{"code":5007,"message":"No such model"}],"result":null}`), nil
		}))

		if result.StopReason != "error" || !strings.Contains(result.ErrorMessage, "Cloudflare Workers AI error: No such model") {
			t.Errorf("result=%+v", result)
		}
	})
}
