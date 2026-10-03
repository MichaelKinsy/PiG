package ai

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The classifier response checks are JavaScript typeof and Array.isArray tests on the parsed body: a null member is not a
// string and not an array (system-one-shared.ts:87, cloudflare-workers-ai-system-one.ts:30 and :54,
// llama-cpp-classify.ts:274, :351 and :380-381). json.Unmarshal alone decodes null into "" or an empty slice.
func TestClassifierResponseNullMembersAreNotStringsOrArrays(t *testing.T) {
	pick := ClassifierContext{State: JsonObject{}, Questions: ClassifierQuestions{{ID: "pick", Question: ClassifierChoiceQuestion{Instructions: "Pick", Criteria: clsChoices("a", "", "b", "")}}}}
	typesafe := ClassifierModel{ID: "jev", API: ClassifierAPITypesafeSystemOne, Provider: "typesafe", BaseURL: "https://api.typesafe.test/v1"}
	cloudflare := ClassifierModel{ID: "typesafe/jev", API: ClassifierAPICloudflareWorkersAISystemOne, Provider: "cloudflare-workers-ai", BaseURL: "https://api.cloudflare.test/ai"}
	reply := func(body string) ClassifierOptions {
		return ClassifierOptions{APIKey: "key", APIKeySet: true, MaxRetries: new(0), Fetch: clsClient(func(*http.Request) (*http.Response, error) { return clsJSON(200, body), nil })}
	}

	t.Run("a null choice is not a choice answer", func(t *testing.T) {
		result := ClassifyTypesafeSystemOne(t.Context(), typesafe, pick, reply(`{"answers":{"pick":{"type":"choice","choice":null,"probabilities":{"a":1,"b":0},"confidence":1}}}`))
		if result.StopReason != ClassifierStopReasonError || result.ErrorMessage != "System One API did not return a choice answer for pick" {
			t.Errorf("result=%+v", result)
		}
	})
	t.Run("the first invalid probability is reported in JavaScript key order", func(t *testing.T) {
		result := ClassifyTypesafeSystemOne(t.Context(), typesafe, pick, reply(`{"answers":{"pick":{"type":"choice","choice":"a","probabilities":{"a":"x","1":"y"},"confidence":1}}}`))
		if result.ErrorMessage != "System One API returned an invalid probability for pick.1" {
			t.Errorf("error=%q", result.ErrorMessage)
		}
	})
	t.Run("Cloudflare error entries without a string message are skipped", func(t *testing.T) {
		result := ClassifyCloudflareWorkersAISystemOne(t.Context(), cloudflare, pick, reply(`{"success":false,"errors":[{"message":null},{"message":"bad input"}]}`))
		if result.ErrorMessage != "Cloudflare Workers AI error: bad input" {
			t.Errorf("error=%q", result.ErrorMessage)
		}
		result = ClassifyCloudflareWorkersAISystemOne(t.Context(), cloudflare, pick, reply(`{"success":false,"errors":null}`))
		if result.ErrorMessage != "Cloudflare Workers AI request failed" {
			t.Errorf("null errors: error=%q", result.ErrorMessage)
		}
	})
	for _, tc := range []struct{ state, want string }{
		{`null`, "null"},
		{`1.0`, "1"},
		{`{"a":1}`, "[object Object]"},
		{`[1,null,"a",[2,3]]`, "1,,a,2,3"},
		{`true`, "true"},
		{`"Running"`, "Running"},
	} {
		t.Run("a run state "+tc.state+" is shown as String(state)", func(t *testing.T) {
			result := ClassifyCloudflareWorkersAISystemOne(t.Context(), cloudflare, pick, reply(`{"success":true,"result":{"state":`+tc.state+`,"result":{}}}`))
			if want := "Cloudflare Workers AI run did not complete (state: " + tc.want + ")"; result.ErrorMessage != want {
				t.Errorf("error=%q want %q", result.ErrorMessage, want)
			}
		})
	}

	llama := func(t *testing.T, path, body string) ClassifierResult {
		t.Helper()
		fetch := clsClient(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path == path {
				return clsJSON(200, body), nil
			}
			switch r.URL.Path {
			case "/tokenize":
				data, _ := json.Marshal(map[string]any{"tokens": charTokens(clsBody(t, r)["content"].(string))})
				return clsJSON(200, string(data)), nil
			case "/apply-template":
				return clsJSON(200, `{"prompt":"prompt\n"}`), nil
			}
			return clsJSON(200, `{"completion_probabilities":[{"top_logprobs":[{"id":65,"logprob":-0.1},{"id":66,"logprob":-2}]}]}`), nil
		})
		return ClassifyLlamaCpp(t.Context(), llamaTestModel(), pick, ClassifierOptions{Fetch: fetch, MaxRetries: new(0)})
	}
	t.Run("llama.cpp baseline answers", func(t *testing.T) {
		if result := llama(t, "", ""); result.StopReason != ClassifierStopReasonStop {
			t.Fatalf("result=%+v", result)
		}
	})
	for _, tc := range []struct{ name, path, body, want string }{
		{"null tokens are not a tokenization", "/tokenize", `{"tokens":null}`, "llama.cpp returned an unexpected tokenization"},
		{"a null prompt is not a prompt", "/apply-template", `{"prompt":null}`, "llama.cpp did not return a prompt"},
		{"null completion probabilities are not token probabilities", "/completion", `{"completion_probabilities":null}`, "llama.cpp did not return token probabilities"},
		{"null top log-probabilities are not token probabilities", "/completion", `{"completion_probabilities":[{"top_logprobs":null}]}`, "llama.cpp did not return token probabilities"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := llama(t, tc.path, tc.body)
			if result.StopReason != ClassifierStopReasonError || !strings.Contains(result.ErrorMessage, tc.want) {
				t.Errorf("result=%+v", result)
			}
		})
	}
}
