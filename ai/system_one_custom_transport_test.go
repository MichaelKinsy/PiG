package ai

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// Pi packages/ai/src/api/system-one-shared.ts classifySystemOne over a caller-supplied SystemOneTransport (:19-28):
//   - wireRequest (:86-96) hands transport.payload { state, questions } with every public `bool` question's type renamed to
//     `noul` (its other members kept, order kept) and choice/score questions unchanged;
//   - a model whose api is not transport.api fails with "Unsupported classifier API: <api>" and an image input with
//     "<label> does not support image input", both before any request (:121-122);
//   - the usage is parsed before the answers (:130-134), so a response whose answers are malformed still reports its usage with
//     stopReason "error" and "<label> did not return an answer for <id>".
func TestSystemOneTransportAndWireRequestDriveClassifySystemOne(t *testing.T) {
	const api = ClassifierAPI("custom-system-one")
	model := ClassifierModel{ID: "m", API: api, Provider: "custom", BaseURL: "https://example.test/v1", Input: []string{"text", "image"}, Cost: ModelCost{Input: 1000}}
	request := ClassifierContext{
		State: JsonObject{"text": "hi"},
		Questions: ClassifierQuestions{
			{ID: "category", Question: ClassifierChoiceQuestion{Instructions: "pick", Criteria: clsChoices("a", "A", "b", "B")}},
			{ID: "approved", Question: ClassifierBoolQuestion{Instructions: "ok?", Criteria: ClassifierBoolCriteria{True: "yes", False: "no"}}},
			{ID: "score", Question: ClassifierScoreQuestion{Instructions: "rate", Criteria: []string{"low", "high"}}},
		},
	}
	var seen []SystemOneWireRequest
	var envelopes []json.RawMessage
	transport := SystemOneTransport{
		API:   api,
		Label: "Custom service",
		URL:   func(ClassifierModel) string { return "https://example.test/v1/classify" },
		Payload: func(_ ClassifierModel, wire SystemOneWireRequest) map[string]any {
			seen = append(seen, wire)
			return map[string]any{"input": wire}
		},
		Output: func(body json.RawMessage) (map[string]json.RawMessage, error) {
			envelopes = append(envelopes, body)
			var envelope struct {
				Result map[string]json.RawMessage `json:"result"`
			}
			err := json.Unmarshal(body, &envelope)
			return envelope.Result, err
		},
	}
	var requests []map[string]any
	client := func(response string) *http.Client {
		return clsClient(func(r *http.Request) (*http.Response, error) {
			requests = append(requests, clsBody(t, r))
			return clsJSON(200, response), nil
		})
	}
	const goodAnswers = `{"category":{"type":"choice","choice":"a","probabilities":{"a":0.7,"b":0.3},"confidence":0.9},"approved":{"type":"noul","noul":0.95},"score":{"type":"score","score":1.5,"confidence":0.4}}`

	ok := ClassifySystemOne(t.Context(), transport, model, request, ClassifierOptions{APIKey: "k", Fetch: client(`{"result":{"answers":` + goodAnswers + `,"usage":{"input_tokens":308,"output_tokens":23}}}`)})
	if ok.StopReason != ClassifierStopReasonStop || ok.Usage == nil || ok.Usage.Input != 308 || ok.Usage.Output != 23 {
		t.Fatalf("result = %+v", ok)
	}
	if got := clsAnswer(t, ok.Answers, "approved"); got != (ClassifierBoolAnswer{Probability: 0.95}) {
		t.Fatalf("approved = %+v, want the noul answer read as a bool probability", got)
	}
	if len(seen) != 1 || len(envelopes) != 1 || len(requests) != 1 {
		t.Fatalf("payload calls %d, output calls %d, requests %d, want 1 each", len(seen), len(envelopes), len(requests))
	}
	wire, err := json.Marshal(seen[0])
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"State":{"text":"hi"},"Questions":{"category":{"type":"choice","instructions":"pick","criteria":{"a":"A","b":"B"}},"approved":{"type":"noul","instructions":"ok?","criteria":{"true":"yes","false":"no"}},"score":{"type":"score","instructions":"rate","criteria":["low","high"]}}}`; string(wire) != want {
		t.Fatalf("wire request = %s\nwant %s", wire, want)
	}
	if _, wrapped := requests[0]["input"]; !wrapped {
		t.Fatalf("request body = %v, want the transport's envelope", requests[0])
	}

	requests = nil
	wrongAPI := model
	wrongAPI.API = "another-api"
	if r := ClassifySystemOne(t.Context(), transport, wrongAPI, request, ClassifierOptions{APIKey: "k", Fetch: client(`{}`)}); r.StopReason != ClassifierStopReasonError || !strings.Contains(r.ErrorMessage, "Unsupported classifier API: another-api") {
		t.Fatalf("wrong api result = %+v", r)
	}
	withImage := request
	withImage.Images = []ImageContent{{Data: "x", MimeType: "image/png"}}
	if r := ClassifySystemOne(t.Context(), transport, model, withImage, ClassifierOptions{APIKey: "k", Fetch: client(`{}`)}); r.StopReason != ClassifierStopReasonError || !strings.Contains(r.ErrorMessage, "Custom service does not support image input") {
		t.Fatalf("image result = %+v", r)
	}
	if len(requests) != 0 {
		t.Fatalf("a request was sent for a failure Pi raises before any request: %v", requests)
	}

	billed := ClassifySystemOne(t.Context(), transport, model, request, ClassifierOptions{APIKey: "k", Fetch: client(`{"result":{"answers":{"category":{"type":"choice","choice":"a","probabilities":{"a":1},"confidence":1}},"usage":{"input_tokens":5,"output_tokens":2}}}`)})
	if billed.StopReason != ClassifierStopReasonError || !strings.Contains(billed.ErrorMessage, "Custom service did not return an answer for approved") || billed.Usage == nil || billed.Usage.Input != 5 || billed.Usage.Output != 2 {
		t.Fatalf("malformed-answers result = %+v, want the usage kept and the missing answer named", billed)
	}
}
