package ai

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"
)

func serializedCompatClone(in *ModelCompat) *ModelCompat {
	if in == nil {
		return nil
	}
	data, err := json.Marshal(in)
	if err != nil {
		return nil
	}
	var out ModelCompat
	if json.Unmarshal(data, &out) != nil {
		return nil
	}
	return &out
}

func TestCloneCompatPreservesSerializedCopy(t *testing.T) {
	values := []*ModelCompat{nil, {}, {OpenRouterRouting: map[string]any{}}, {AllowedFallbackModels: []AnthropicAllowedFallbackModel{}}, {ChatTemplateArgs: map[string]any{"n": 42, "nested": []any{"x"}}}, {VLLMPriority: new(math.NaN())}, {ThinkingFormat: string([]byte{0xff})}}
	for _, model := range GeneratedModels {
		values = append(values, model.Compat)
	}
	for _, value := range values {
		got, want := cloneCompat(value), serializedCompatClone(value)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("clone changed serialization semantics for %+v", value)
		}
		if value == nil || got == nil {
			continue
		}
		original, copyValue := reflect.ValueOf(value).Elem(), reflect.ValueOf(got).Elem()
		for i := 0; i < original.NumField(); i++ {
			if original.Field(i).Kind() == reflect.Pointer && !original.Field(i).IsNil() && original.Field(i).Pointer() == copyValue.Field(i).Pointer() {
				t.Fatalf("clone shares %s", original.Type().Field(i).Name)
			}
		}
	}
}

func TestCloneCompatAvoidsScalarJSONRoundTrip(t *testing.T) {
	value := &ModelCompat{SupportsStore: new(false), SupportsReasoningEffort: new(true), MaxTokensField: "max_tokens", VLLMPriority: new(1.25)}
	reference := testing.AllocsPerRun(100, func() { _ = serializedCompatClone(value) })
	got := testing.AllocsPerRun(100, func() { _ = cloneCompat(value) })
	if got >= reference {
		t.Fatalf("scalar clone uses %.0f allocations; JSON round trip uses %.0f", got, reference)
	}
}

func BenchmarkCloneCatalogCompat(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		for _, model := range GeneratedModels {
			_ = cloneCompat(model.Compat)
		}
	}
}
