package ai

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// The JSON forms of the classifier and images values carry the extension wire (extensions/sdk model registry classify, provider_operation): an object keyed by question or answer id keeps the order of its entries, and a result decodes to what it encoded from.
func TestClassifierContextAndResultRoundTripKeepOrder(t *testing.T) {
	context := ClassifierContext{State: JsonObject{"text": "hi"}, Questions: ClassifierQuestions{
		{ID: "zulu", Question: ClassifierBoolQuestion{Instructions: "Z?", Criteria: ClassifierBoolCriteria{True: "yes", False: "no"}}},
		{ID: "alpha", Question: ClassifierScoreQuestion{Instructions: "A?", Criteria: []string{"low", "high"}}},
		{ID: "mid", Question: ClassifierChoiceQuestion{Instructions: "M?", Criteria: []ClassifierChoiceCriterion{{Key: "b", Description: "B"}, {Key: "a", Description: "A"}}}},
	}}
	encoded, err := json.Marshal(context)
	if err != nil {
		t.Fatal(err)
	}
	var decoded ClassifierContext
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, context) {
		t.Fatalf("context = %+v\nwant %+v\nwire %s", decoded, context, encoded)
	}

	result := ClassifierResult{API: "system-one", Provider: "p", Model: "m", StopReason: ClassifierStopReasonStop, Timestamp: 7, Usage: &Usage{Input: 3, TotalTokens: 3}, Answers: ClassifierAnswers{
		{ID: "zulu", Answer: ClassifierBoolAnswer{Probability: 0.25}},
		{ID: "alpha", Answer: ClassifierScoreAnswer{Score: 2, Confidence: 0.5}},
		{ID: "mid", Answer: ClassifierChoiceAnswer{Choice: "b", Probabilities: []ClassifierProbability{{Key: "b", Probability: 0.75}, {Key: "a", Probability: 0.25}}, Confidence: 0.5}},
	}}
	encoded, err = json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var back ClassifierResult
	if err := json.Unmarshal(encoded, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back, result) {
		t.Fatalf("result = %+v\nwant %+v\nwire %s", back, result, encoded)
	}
	// An error result carries no answers but the answers object.
	failed := ClassifierResult{API: "system-one", Provider: "p", Model: "m", StopReason: ClassifierStopReasonError, ErrorMessage: "boom", Answers: ClassifierAnswers{}}
	encoded, _ = json.Marshal(failed)
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &wire); err != nil || string(wire["answers"]) != "{}" || string(wire["errorMessage"]) != `"boom"` {
		t.Fatalf("failed = %s, %v", encoded, err)
	}
	if err := json.Unmarshal([]byte(`{"answers":{"q":{"type":"video"}}}`), &back); err == nil {
		t.Fatal("an answer of an unknown type must not decode")
	}
}

func TestImagesContextAndResultRoundTrip(t *testing.T) {
	request := ImagesContext{Input: []ContentBlock{TextContent{Text: "a red circle"}}}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var decoded ImagesContext
	if err := json.Unmarshal(encoded, &decoded); err != nil || !reflect.DeepEqual(decoded, request) {
		t.Fatalf("context = %+v, %v (wire %s)", decoded, err, encoded)
	}
	result := AssistantImages{API: "images", Provider: "p", Model: "m", Output: []ContentBlock{ImageContent{Data: "aGk=", MimeType: "image/png"}}, ResponseID: "r-1", StopReason: ImagesStopReasonStop, Timestamp: 9}
	encoded, err = json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var back AssistantImages
	if err := json.Unmarshal(encoded, &back); err != nil || !reflect.DeepEqual(back, result) {
		t.Fatalf("result = %+v, %v (wire %s)", back, err, encoded)
	}
	empty, _ := json.Marshal(AssistantImages{StopReason: ImagesStopReasonError})
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(empty, &wire); err != nil || string(wire["output"]) != "[]" {
		t.Fatalf("an empty result must carry output []: %s", empty)
	}
}

// ClassifierContext.images crosses the extension wire between the state and the questions, and is omitted when absent.
func TestClassifierContextImagesWire(t *testing.T) {
	question := ClassifierQuestions{{ID: "q", Question: ClassifierBoolQuestion{Instructions: "Q?", Criteria: ClassifierBoolCriteria{True: "y", False: "n"}}}}
	withImages := ClassifierContext{State: JsonObject{"a": 1.0}, Images: []ImageContent{{Data: "aW1hZ2U=", MimeType: "image/png"}}, Questions: question}
	encoded, err := json.Marshal(withImages)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"state":{"a":1},"images":[{"type":"image","data":"aW1hZ2U=","mimeType":"image/png"}],"questions":{"q":{"type":"bool","instructions":"Q?","criteria":{"true":"y","false":"n"}}}}`
	if string(encoded) != want {
		t.Fatalf("wire = %s\nwant %s", encoded, want)
	}
	var decoded ClassifierContext
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, withImages) {
		t.Fatalf("decoded = %+v", decoded)
	}
	// An absent images member is omitted; an empty array is written, as JSON.stringify writes it (types.ts:669-677).
	absent, _ := json.Marshal(ClassifierContext{State: JsonObject{}, Questions: question})
	if strings.Contains(string(absent), "images") {
		t.Fatalf("absent images written: %s", absent)
	}
	empty, _ := json.Marshal(ClassifierContext{State: JsonObject{}, Images: []ImageContent{}, Questions: question})
	if !strings.Contains(string(empty), `"images":[]`) {
		t.Fatalf("empty images omitted: %s", empty)
	}
}
