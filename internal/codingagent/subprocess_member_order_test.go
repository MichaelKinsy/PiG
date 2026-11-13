package codingagent

// pi: packages/ai/src/compat/stream.ts (an extension streams with its own Model object and options) and
// packages/ai/src/api/openai-completions.ts:994-997 (the caller's samplingParams object is assigned to the request).

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/orderedjson"
)

// A model object an extension hands to a built-in API keeps the member order of its sampling parameters and compat objects when it
// arrives as the JSON text the extension wrote.
func TestAPIModelOfJSONKeepsSuppliedMemberOrder(t *testing.T) {
	raw := `{"id":"m","name":"M","api":"openai-completions","provider":"p","baseUrl":"https://x.test/v1","reasoning":false,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":1000,"maxTokens":100,` +
		`"compat":{"openRouterRouting":{"order":["a"],"only":["b"],"max_price":{"prompt":1,"completion":2}}},"samplingParams":{"top_p":0.5,"min_p":0.1},"samplingParamsByThinkingLevel":{"off":{"top_k":7,"mirostat":1}}}`
	model, err := apiModelOfJSON(json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"top_p", "min_p"}; !slices.Equal(model.SamplingParamsOrder, want) {
		t.Errorf("SamplingParamsOrder = %v, want %v", model.SamplingParamsOrder, want)
	}
	if want := (map[ai.ModelThinkingLevel][]string{ai.ThinkingOff: {"top_k", "mirostat"}}); !reflect.DeepEqual(model.SamplingParamsByThinkingLevelOrder, want) {
		t.Errorf("SamplingParamsByThinkingLevelOrder = %v, want %v", model.SamplingParamsByThinkingLevelOrder, want)
	}
	compat, err := json.Marshal(model.ProviderMeta.Compat)
	if err != nil {
		t.Fatal(err)
	}
	if want := `"openRouterRouting":{"order":["a"],"only":["b"],"max_price":{"prompt":1,"completion":2}}`; !strings.Contains(string(compat), want) {
		t.Errorf("compat = %s, want it to hold %s", compat, want)
	}
}

// The subprocess bridge hands a request's samplingParams on as an ordered object when the extension listed its members in another order
// than sorted; the stream options then carry that order beside the map. A decoded map carries none.
func TestSubprocessStreamOptionsKeepSamplingParamsOrder(t *testing.T) {
	ordered, err := orderedjson.Parse([]byte(`{"top_p":0.9,"min_p":0.1,"seed":1}`))
	if err != nil {
		t.Fatal(err)
	}
	options, err := subprocessStreamOptions(map[string]any{"samplingParams": ordered}, ai.StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"top_p", "min_p", "seed"}; !slices.Equal(options.SamplingParamsOrder, want) {
		t.Errorf("SamplingParamsOrder = %v, want %v", options.SamplingParamsOrder, want)
	}
	if want := (map[string]any{"top_p": 0.9, "min_p": 0.1, "seed": float64(1)}); !reflect.DeepEqual(options.SamplingParams, want) {
		t.Errorf("SamplingParams = %v, want %v", options.SamplingParams, want)
	}
	options, err = subprocessStreamOptions(map[string]any{"samplingParams": map[string]any{"top_p": 0.9, "min_p": 0.1}}, ai.StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if options.SamplingParamsOrder != nil || len(options.SamplingParams) != 2 {
		t.Errorf("a decoded map gives order %v and parameters %v", options.SamplingParamsOrder, options.SamplingParams)
	}
}
