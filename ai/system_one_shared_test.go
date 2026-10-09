package ai

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// packages/ai/src/api/system-one-shared.ts:63-146 (RequiredNumber, probabilities, parseAnswers, tokenCount, parseUsage)
// and packages/ai/src/api/cloudflare-workers-ai-system-one.ts:5-13 (error message).
func TestSystemOneSharedAnswerValidation(t *testing.T) {
	const ok = `"satisfaction":{"type":"score","score":2,"confidence":0.7},"approved":{"type":"noul","noul":0.95}`
	for _, tc := range []struct{ name, answers, want string }{
		{"missing answer", `{"satisfaction":{"type":"score","score":2,"confidence":0.7},"approved":{"type":"noul","noul":0.95}}`, "System One API did not return an answer for category"},
		{"answer is not an object", `{"category":[],` + ok + `}`, "System One API did not return an answer for category"},
		{"choice with a wrong type", `{"category":{"type":"score","choice":"a","probabilities":{},"confidence":1},` + ok + `}`, "System One API did not return a choice answer for category"},
		{"choice that is not a string", `{"category":{"type":"choice","choice":1,"probabilities":{},"confidence":1},` + ok + `}`, "System One API did not return a choice answer for category"},
		{"probabilities that are not an object", `{"category":{"type":"choice","choice":"a","probabilities":[],"confidence":1},` + ok + `}`, "System One API returned invalid probabilities for category"},
		{"non-numeric probability", `{"category":{"type":"choice","choice":"a","probabilities":{"a":"x"},"confidence":1},` + ok + `}`, "System One API returned an invalid probability for category.a"},
		{"missing confidence", `{"category":{"type":"choice","choice":"a","probabilities":{"a":1}},` + ok + `}`, "System One API returned an invalid confidence for category"},
		{"score with a wrong type", `{"category":{"type":"choice","choice":"a","probabilities":{"a":1},"confidence":1},"satisfaction":{"type":"noul","score":2,"confidence":1},"approved":{"type":"noul","noul":1}}`, "System One API did not return a score answer for satisfaction"},
		{"non-numeric score", `{"category":{"type":"choice","choice":"a","probabilities":{"a":1},"confidence":1},"satisfaction":{"type":"score","score":"2","confidence":1},"approved":{"type":"noul","noul":1}}`, "System One API returned an invalid score for satisfaction"},
		{"bool answered as bool", `{"category":{"type":"choice","choice":"a","probabilities":{"a":1},"confidence":1},"satisfaction":{"type":"score","score":2,"confidence":1},"approved":{"type":"bool","noul":1}}`, "System One API did not return a bool answer for approved"},
		{"non-numeric noul", `{"category":{"type":"choice","choice":"a","probabilities":{"a":1},"confidence":1},"satisfaction":{"type":"score","score":2,"confidence":1},"approved":{"type":"noul","noul":null}}`, "System One API returned an invalid probability for approved"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := ClassifyTypesafeSystemOne(t.Context(), typesafeTestModel(), typesafeTestContext(), ClassifierOptions{APIKey: "k", Fetch: clsClient(func(*http.Request) (*http.Response, error) {
				return clsJSON(200, `{"answers":`+tc.answers+`}`), nil
			})})
			if result.StopReason != "error" || result.ErrorMessage != tc.want {
				t.Fatalf("stop=%q message=%q, want error %q", result.StopReason, result.ErrorMessage, tc.want)
			}
		})
	}
}

func TestSystemOneSharedUsage(t *testing.T) {
	model := typesafeTestModel()
	model.Cost.Input, model.Cost.Output = 1_000_000, 2_000_000
	for _, tc := range []struct {
		name, usage string
		want        *[3]int
	}{
		{"absent", ``, nil},
		{"not an object", `,"usage":[1]`, nil},
		{"neither count present", `,"usage":{"cost":1}`, nil},
		{"input only", `,"usage":{"input_tokens":5}`, &[3]int{5, 0, 5}},
		{"negative, non-finite and non-numeric counts are zero", `,"usage":{"input_tokens":-3,"output_tokens":"7"}`, &[3]int{0, 0, 0}},
		{"output only", `,"usage":{"output_tokens":7}`, &[3]int{0, 7, 7}},
		{"both", `,"usage":{"input_tokens":5,"output_tokens":7}`, &[3]int{5, 7, 12}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := ClassifyTypesafeSystemOne(t.Context(), model, typesafeTestContext(), ClassifierOptions{APIKey: "k", Fetch: clsClient(func(*http.Request) (*http.Response, error) {
				return clsJSON(200, `{"answers":`+typesafeWireAnswers+tc.usage+`}`), nil
			})})
			if result.StopReason != "stop" {
				t.Fatalf("result=%+v", result)
			}
			if tc.want == nil {
				if result.Usage != nil {
					t.Fatalf("usage=%+v, want none", result.Usage)
				}
				return
			}
			if result.Usage == nil || [3]int{result.Usage.Input, result.Usage.Output, result.Usage.TotalTokens} != *tc.want {
				t.Fatalf("usage=%+v, want %v", result.Usage, *tc.want)
			}
			if want := float64(tc.want[0]) + 2*float64(tc.want[1]); result.Usage.Cost.Total != want {
				t.Fatalf("cost=%v, want %v", result.Usage.Cost.Total, want)
			}
		})
	}
}

func TestCloudflareSystemOneEnvelopeErrors(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"messages are joined with a semicolon", `{"success":false,"errors":[{"message":"a"},{"code":1},{"message":"b"}],"result":null}`, "Cloudflare Workers AI error: a; b"},
		{"no message", `{"success":false,"errors":[{"code":1}],"result":null}`, "Cloudflare Workers AI request failed"},
		{"errors is not an array", `{"success":false,"errors":"x","result":null}`, "Cloudflare Workers AI request failed"},
		{"result is not an object", `{"success":true,"result":[]}`, "Cloudflare Workers AI returned an unexpected response"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			models, jev := cloudflareClassifierSetup(t)
			result := models.Classify(t.Context(), jev, cloudflareTestContext(), cloudflareClassifierOptions(func(*http.Request) (*http.Response, error) {
				return clsJSON(200, tc.body), nil
			}))
			if result.StopReason != "error" || !strings.Contains(result.ErrorMessage, tc.want) {
				t.Fatalf("result=%+v, want %q", result, tc.want)
			}
		})
	}
}

// classifier-shared.ts postClassifierRequest (Pi 1.1.0, shared by System One and Decisions): onPayload may replace the
// body before it is sent, and onResponse sees the status and headers of the response that was parsed.
func TestSystemOneSharedRequestHooks(t *testing.T) {
	model := typesafeTestModel()
	var sent string
	var seen []ProviderResponse
	result := ClassifyTypesafeSystemOne(t.Context(), model, typesafeTestContext(), ClassifierOptions{
		APIKey: "k",
		OnPayload: func(any, ClassifierModel) (any, bool, error) {
			return map[string]any{"replaced": true}, true, nil
		},
		OnResponse: func(response ProviderResponse, _ ClassifierModel) error {
			seen = append(seen, response)
			return nil
		},
		Fetch: clsClient(func(request *http.Request) (*http.Response, error) {
			body, _ := io.ReadAll(request.Body)
			sent = string(body)
			response := clsJSON(200, `{"answers":`+typesafeWireAnswers+`}`)
			response.Header.Set("x-request-id", "r1")
			return response, nil
		}),
	})
	if result.StopReason != "stop" {
		t.Fatalf("result=%+v", result)
	}
	if sent != `{"replaced":true}` {
		t.Fatalf("sent %s, want the payload onPayload returned", sent)
	}
	if len(seen) != 1 || seen[0].Status != 200 || seen[0].Headers["x-request-id"] != "r1" {
		t.Fatalf("onResponse saw %+v, want one 200 response with its headers", seen)
	}
}

// classifier-shared.ts postClassifierRequest: a response that is not JSON fails the request with V8's JSON.parse message, and a
// non-2xx response fails it with "<label> returned <status>" carrying its status and body.
func TestSystemOneSharedFailureMessages(t *testing.T) {
	model := typesafeTestModel()
	post := func(status int, body string) error {
		response := clsText(status, body, nil)
		defer func() { _ = response.Body.Close() }()
		retries := 0
		_, err := PostClassifierRequest(t.Context(), "Label", "http://example.invalid/x", model, map[string]any{}, ClassifierOptions{
			APIKey: "k", MaxRetries: &retries, Fetch: clsClient(func(*http.Request) (*http.Response, error) { return response, nil }),
		}, nil)
		return err
	}
	if err := post(200, "<html>"); err == nil || err.Error() != `Unexpected token '<', "<html>" is not valid JSON` {
		t.Fatalf("non-JSON body error = %v, want V8's JSON.parse message (Pi's Response.json)", err)
	}
	err := post(400, "bad request")
	var httpErr *ClassifierHTTPError
	if !errors.As(err, &httpErr) || err.Error() != "Label returned 400" || httpErr.Body != "bad request" || httpErr.Status == nil || *httpErr.Status != 400 {
		t.Fatalf("400 error = %v (%+v), want 'Label returned 400' with its status and body", err, httpErr)
	}
}
