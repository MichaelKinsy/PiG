package subprocess

// pi: packages/ai/src/api/openai-completions.ts:994-997 (the caller's samplingParams object is assigned to the request, so its member
// order is on the wire).

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/orderedjson"
)

// A model stream request an extension sends keeps the member order of its samplingParams: an object whose members are not sorted is
// handed on as an ordered object, and a sorted one stays the decoded map.
func TestDecodeModelStreamRequestKeepsSamplingParamsOrder(t *testing.T) {
	request, err := decodeModelStreamRequest([]byte(`{"samplingParams":{"top_p":0.9,"min_p":0.1,"seed":1}}`))
	if err != nil {
		t.Fatal(err)
	}
	ordered, ok := request["samplingParams"].(*orderedjson.Object)
	if !ok {
		t.Fatalf("samplingParams = %T, want the ordered object", request["samplingParams"])
	}
	if want := []string{"top_p", "min_p", "seed"}; !slices.Equal(ordered.Keys(), want) {
		t.Errorf("samplingParams members = %v, want %v", ordered.Keys(), want)
	}
	request, err = decodeModelStreamRequest([]byte(`{"samplingParams":{"min_p":0.1,"top_p":0.9}}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := request["samplingParams"].(map[string]any); !ok {
		t.Errorf("sorted samplingParams = %T, want the decoded map", request["samplingParams"])
	}
}
