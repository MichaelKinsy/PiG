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
	}{{"@cf/cloudflare/clef", 0.24}, {"@cf/cloudflare/clef-flash", 0.09}} {
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
