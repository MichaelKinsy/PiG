package ai

import (
	"reflect"
	"slices"
	"testing"
)

// packages/ai/src/types.ts: `ThinkingLevel` is "minimal" | ... | "max" (no "off"), `ModelThinkingLevel` is "off" | ThinkingLevel, and `SimpleStreamOptions.reasoning` is a `ThinkingLevel` that may be omitted.
func TestThinkingLevelExcludesOffAndModelThinkingLevelIncludesIt(t *testing.T) {
	upstream := upstreamStringUnion(t, "types.ts", "ThinkingLevel")
	levels := []ThinkingLevel{ThinkingLevelMinimal, ThinkingLevelLow, ThinkingLevelMedium, ThinkingLevelHigh, ThinkingLevelXHigh, ThinkingLevelMax}
	var got []string
	for _, level := range levels {
		got = append(got, string(level))
	}
	if !slices.Equal(got, upstream) || slices.Contains(upstream, "off") {
		t.Errorf("ThinkingLevel constants %v, upstream %v", got, upstream)
	}
	modelLevels := []ModelThinkingLevel{ThinkingOff, ThinkingMinimal, ThinkingLow, ThinkingMedium, ThinkingHigh, ThinkingXHigh, ThinkingMax}
	got = nil
	for _, level := range modelLevels {
		got = append(got, string(level))
	}
	if want := append([]string{"off"}, upstream...); !slices.Equal(got, want) {
		t.Errorf("ModelThinkingLevel constants %v, upstream %v", got, want)
	}
	thinkingLevelType, modelLevelType := reflect.TypeFor[ThinkingLevel](), reflect.TypeFor[ModelThinkingLevel]()
	if thinkingLevelType == modelLevelType {
		t.Fatal("ThinkingLevel and ModelThinkingLevel are one type")
	}
	if reflect.TypeOf(ThinkingOff) != modelLevelType || reflect.TypeOf(ThinkingOff).AssignableTo(thinkingLevelType) {
		t.Errorf("ThinkingOff has type %v", reflect.TypeOf(ThinkingOff))
	}
	if field, _ := reflect.TypeFor[StreamOptions]().FieldByName("Thinking"); field.Type != thinkingLevelType {
		t.Errorf("StreamOptions.Thinking has type %v", field.Type)
	}
	if field, _ := reflect.TypeFor[ModelCapabilities]().FieldByName("MaxThinking"); field.Type != thinkingLevelType {
		t.Errorf("ModelCapabilities.MaxThinking has type %v", field.Type)
	}
}

// packages/ai/src/models.ts getSupportedThinkingLevels and clampThinkingLevel work on ModelThinkingLevel, so "off" is a supported level and the clamp target of a model without reasoning.
func TestSupportedAndClampedThinkingLevelsAreModelThinkingLevels(t *testing.T) {
	plain := &Model{ID: "plain"}
	var supported []ModelThinkingLevel = GetSupportedThinkingLevels(plain)
	if !slices.Equal(supported, []ModelThinkingLevel{ThinkingOff}) {
		t.Errorf("supported = %v", supported)
	}
	var clamped ModelThinkingLevel = ClampThinkingLevel(plain, ThinkingHigh)
	if clamped != ThinkingOff {
		t.Errorf("clamped = %q", clamped)
	}
	reasoning := &Model{ID: "r", ProviderMeta: ProviderMetadata{Reasoning: true}}
	if got := ClampThinkingLevel(reasoning, ModelThinkingLevel(ThinkingMedium)); got != ThinkingMedium {
		t.Errorf("clamp medium = %q", got)
	}
}

// packages/ai/src/api/simple-options.ts buildBaseOptions resolves sampling with `options?.reasoning ?? "off"`, and `reasoning` appears on the wire only when set.
func TestOmittedReasoningResolvesLikeOff(t *testing.T) {
	model := &Model{ID: "m", ProviderMeta: ProviderMetadata{Reasoning: true}, SamplingParamsByThinkingLevel: SamplingParamsByThinkingLevel{ThinkingOff: {"temperature": 0.1}, ThinkingHigh: {"temperature": 0.9}}}
	if got := ResolveSamplingParams(model, ThinkingOff, nil); got["temperature"] != 0.1 {
		t.Errorf("off = %v", got)
	}
	if got := ResolveSamplingParams(model, ModelThinkingLevel(ThinkingHigh), nil); got["temperature"] != 0.9 {
		t.Errorf("high = %v", got)
	}
	field, _ := reflect.TypeFor[StreamOptions]().FieldByName("Thinking")
	if tag := field.Tag.Get("json"); tag != "reasoning,omitempty" {
		t.Errorf("StreamOptions.Thinking json tag = %q", tag)
	}
}

// packages/agent/src/agent-loop.ts `reasoning: config.reasoning === "off" ? undefined : config.reasoning`: a model level becomes the request option, with "off" meaning no request.
func TestReasoningOptionOmitsOff(t *testing.T) {
	for level, want := range map[ModelThinkingLevel]ThinkingLevel{ThinkingOff: "", ThinkingMinimal: "minimal", ThinkingHigh: "high", ThinkingXHigh: "xhigh", ThinkingMax: "max", "": ""} {
		if got := level.ReasoningOption(); got != want {
			t.Errorf("%q.ReasoningOption() = %q, want %q", level, got, want)
		}
	}
}

// packages/ai/src/api/anthropic-messages.ts and google-generative-ai.ts streamSimple: omitted `reasoning` disables Anthropic thinking (`thinkingEnabled: false`) and sends Google `thinking: { enabled: false }`; a given level, native Google thinking and other APIs are left alone.
func TestApplyOmittedReasoning(t *testing.T) {
	anthropic := &Model{ProviderMeta: ProviderMetadata{API: APIAnthropicMessages}}
	google := &Model{ProviderMeta: ProviderMetadata{API: APIGoogleGenerativeAI}}
	other := &Model{ProviderMeta: ProviderMetadata{API: APIOpenAICompletions}}

	options := StreamOptions{}
	ApplyOmittedReasoning(anthropic, &options)
	if options.ThinkingEnabled == nil || *options.ThinkingEnabled || options.GoogleThinking != nil {
		t.Errorf("anthropic = %+v", options)
	}
	options = StreamOptions{}
	ApplyOmittedReasoning(&Model{ProviderMeta: ProviderMetadata{API: APIGoogleVertex}}, &options)
	if options.GoogleThinking == nil || options.GoogleThinking.Enabled {
		t.Errorf("vertex = %+v", options)
	}
	options = StreamOptions{}
	ApplyOmittedReasoning(google, &options)
	if options.GoogleThinking == nil || options.GoogleThinking.Enabled || options.ThinkingEnabled != nil {
		t.Errorf("google = %+v", options)
	}
	native := &GoogleThinkingOptions{Enabled: true, BudgetTokens: new(7)}
	options = StreamOptions{GoogleThinking: native}
	ApplyOmittedReasoning(google, &options)
	if options.GoogleThinking != native {
		t.Errorf("native Google thinking replaced: %+v", options.GoogleThinking)
	}
	for _, model := range []*Model{anthropic, google, other} {
		options = StreamOptions{Thinking: ThinkingLevelLow}
		ApplyOmittedReasoning(model, &options)
		if options.ThinkingEnabled != nil || options.GoogleThinking != nil {
			t.Errorf("%s changed a given level: %+v", model.ProviderMeta.API, options)
		}
	}
	options = StreamOptions{}
	ApplyOmittedReasoning(other, &options)
	if options.ThinkingEnabled != nil || options.GoogleThinking != nil {
		t.Errorf("other API changed: %+v", options)
	}
}
