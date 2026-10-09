//go:build !pig_strip_google_vertex

package ai

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestResolveGoogleThinkingLevelUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/ai/test/google-thinking-level-map.test.ts:101
	model := &Model{ID: "gemini-3.7-flash", ProviderMeta: ProviderMetadata{ProviderID: "test-google"}}
	for _, level := range []ModelThinkingLevel{ThinkingMinimal, ThinkingLow, ThinkingMedium, ThinkingHigh} {
		got, err := resolveGoogleThinkingLevel(model, level)
		if err != nil || string(got) != string(level) {
			t.Fatalf("default %s = %s, %v", level, got, err)
		}
	}
	for _, mapped := range []string{"minimal", "low", "medium", "high", "MINIMAL", "LOW", "MEDIUM", "HIGH"} {
		for _, level := range []ModelThinkingLevel{ThinkingHigh, ThinkingXHigh, ThinkingMax} {
			model.ThinkingLevelMap = ThinkingLevelMap{ThinkingHigh: new(mapped), ThinkingXHigh: new(mapped), ThinkingMax: new(mapped)}
			got, err := resolveGoogleThinkingLevel(model, level)
			if err != nil || string(got) != strings.ToLower(mapped) {
				t.Fatalf("mapped %s/%s = %s, %v", level, mapped, got, err)
			}
		}
	}
	model.ThinkingLevelMap = ThinkingLevelMap{ThinkingXHigh: new("extreme")}
	if _, err := resolveGoogleThinkingLevel(model, ThinkingXHigh); err == nil || err.Error() != "Unsupported Google thinking level mapping for test-google/gemini-3.7-flash: xhigh -> extreme" {
		t.Fatalf("invalid mapping: %v", err)
	}
	model.ThinkingLevelMap = ThinkingLevelMap{}
	if _, err := resolveGoogleThinkingLevel(model, ThinkingMax); err == nil || err.Error() != "Unsupported Google thinking level mapping for test-google/gemini-3.7-flash: max -> undefined" {
		t.Fatalf("missing mapping: %v", err)
	}
}

func TestGoogleThinkingLevelMapPayloadUpstream(t *testing.T) {
	standard := ThinkingLevelMap{ThinkingOff: nil, ThinkingMinimal: nil, ThinkingLow: new("low"), ThinkingMedium: new("medium"), ThinkingHigh: new("high"), ThinkingXHigh: nil, ThinkingMax: nil}
	for _, vertex := range []bool{false, true} {
		adapter := "Google Generative AI"
		if vertex {
			adapter = "Google Vertex"
		}
		for _, tc := range []struct {
			name, id string
			mapping  ThinkingLevelMap
			level    ModelThinkingLevel
			want     string
		}{
			// .upstream/v0.87.1/packages/ai/test/google-thinking-level-map.test.ts:139
			{"uses the lowest supported level when reasoning is omitted", "gemini-3.8-flash", standard, "", `{"thinkingLevel":"LOW"}`},
			// .upstream/v0.87.1/packages/ai/test/google-thinking-level-map.test.ts:153
			{"preserves native medium effort for Gemini 3.1 Pro", "gemini-3.1-pro-preview", standard, ThinkingMedium, `{"includeThoughts":true,"thinkingLevel":"MEDIUM"}`},
			// .upstream/v0.87.1/packages/ai/test/google-thinking-level-map.test.ts:163
			{"disables Gemini 2.5 thinking when reasoning is omitted", "gemini-2.5-flash", ThinkingLevelMap{}, "", `{"thinkingBudget":0}`},
		} {
			t.Run(adapter+"/"+tc.name, func(t *testing.T) {
				assertShapeJSON(t, captureGoogleThinkingMap(t, vertex, tc.id, tc.mapping, tc.level, nil), tc.want)
			})
		}
	}
	// .upstream/v0.87.1/packages/ai/test/google-thinking-level-map.test.ts:169
	for _, level := range []ModelThinkingLevel{ThinkingXHigh, ThinkingMax} {
		t.Run("maps Google Generative AI "+string(level)+" to a supported level", func(t *testing.T) {
			assertShapeJSON(t, captureGoogleThinkingMap(t, false, "gemini-3.7-flash", ThinkingLevelMap{ThinkingXHigh: new("high"), ThinkingMax: new("high")}, level, nil), `{"includeThoughts":true,"thinkingLevel":"HIGH"}`)
		})
	}
	// .upstream/v0.87.1/packages/ai/test/google-thinking-level-map.test.ts:178
	t.Run("honors uppercase provider values for standard Google Generative AI levels", func(t *testing.T) {
		assertShapeJSON(t, captureGoogleThinkingMap(t, false, "gemini-3.7-flash", ThinkingLevelMap{ThinkingHigh: new("LOW")}, ThinkingHigh, nil), `{"includeThoughts":true,"thinkingLevel":"LOW"}`)
	})
	// .upstream/v0.87.1/packages/ai/test/google-thinking-level-map.test.ts:184
	t.Run("uses mapped Google Generative AI levels for token budgets", func(t *testing.T) {
		assertShapeJSON(t, captureGoogleThinkingMap(t, false, "gemini-2.5-flash", ThinkingLevelMap{ThinkingXHigh: new("high")}, ThinkingXHigh, &ThinkingBudgets{High: 1234}), `{"includeThoughts":true,"thinkingBudget":1234}`)
	})
	// .upstream/v0.87.1/packages/ai/test/google-thinking-level-map.test.ts:192
	t.Run("maps Google Vertex extended levels", func(t *testing.T) {
		assertShapeJSON(t, captureGoogleThinkingMap(t, true, "gemini-3.7-flash", ThinkingLevelMap{ThinkingXHigh: new("high")}, ThinkingXHigh, nil), `{"includeThoughts":true,"thinkingLevel":"HIGH"}`)
	})
	// .upstream/v0.87.1/packages/ai/test/google-thinking-level-map.test.ts:198
	t.Run("uses mapped Google Vertex levels for token budgets", func(t *testing.T) {
		assertShapeJSON(t, captureGoogleThinkingMap(t, true, "gemini-2.5-flash", ThinkingLevelMap{ThinkingMax: new("high")}, ThinkingMax, &ThinkingBudgets{High: 4321}), `{"includeThoughts":true,"thinkingBudget":4321}`)
	})
}

func captureGoogleThinkingMap(t *testing.T, vertex bool, id string, mapping ThinkingLevelMap, level ModelThinkingLevel, budgets *ThinkingBudgets) json.RawMessage {
	t.Helper()
	var provider Provider
	if vertex {
		provider = NewGoogleVertexProvider(GoogleVertexConfig{APIKey: "test", Model: id, ProviderID: "test-vertex", BaseURL: "https://example.invalid/v1", ThinkingLevelMap: mapping})
	} else {
		provider = NewGoogleProvider(GoogleConfig{APIKey: "test", Model: id, ProviderID: "test-google", BaseURL: "https://example.invalid/v1beta", ThinkingLevelMap: mapping})
	}
	captured := errors.New("payload captured")
	var payload json.RawMessage
	// The upstream cases invoke streamSimple, which explicitly lowers omitted reasoning to off before the native call.
	if level == "" {
		level = ThinkingOff
	}
	options := StreamOptions{IsReasoning: true, Thinking: level.ReasoningOption(), ThinkingBudgets: budgets}
	if level == ThinkingOff {
		options.GoogleThinking = &GoogleThinkingOptions{}
	}
	options.OnPayload = func(value any, _ *Model) (any, error) {
		request := value.(map[string]any)
		config := request["config"].(map[string]any)
		var err error
		payload, err = json.Marshal(config["thinkingConfig"])
		if err != nil {
			return nil, err
		}
		return nil, captured
	}
	_, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Hello")}}}), options)
	if !errors.Is(err, captured) {
		t.Fatalf("capture error=%v", err)
	}
	return payload
}

func BenchmarkGoogleMappedThinkingRequest(b *testing.B) {
	provider := NewGoogleProvider(GoogleConfig{APIKey: "test", Model: "gemini-3.8-flash", ProviderID: "test-google", ThinkingLevelMap: ThinkingLevelMap{ThinkingOff: nil, ThinkingMinimal: nil, ThinkingLow: new("low"), ThinkingMedium: new("medium"), ThinkingHigh: new("high")}})
	transcript := NormalizeContext(Context{SystemPrompt: "You are a helpful assistant.", Messages: []Message{UserMessage{Content: UserText("Explain the next step.")}}})
	captured := errors.New("payload captured")
	options := StreamOptions{IsReasoning: true, Thinking: ThinkingLevelMedium, OnPayload: func(_ any, _ *Model) (any, error) { return nil, captured }}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := provider.Stream(b.Context(), transcript, options); !errors.Is(err, captured) {
			b.Fatal(err)
		}
	}
}

func TestGoogleThinkingLevelMapRegression(t *testing.T) {
	for _, tc := range []struct {
		name, id string
		mapping  ThinkingLevelMap
		level    ModelThinkingLevel
		want     string
		include  bool
	}{
		// .upstream/v0.87.1/packages/ai/test/google-thinking-level-map.test.ts:139
		{"uses the lowest supported level when reasoning is omitted", "gemini-3.8-flash", ThinkingLevelMap{ThinkingOff: nil, ThinkingMinimal: nil, ThinkingLow: new("low"), ThinkingMedium: new("medium"), ThinkingHigh: new("high"), ThinkingXHigh: nil, ThinkingMax: nil}, "", "LOW", false},
		// .upstream/v0.87.1/packages/ai/test/google-thinking-level-map.test.ts:153
		{"preserves native medium effort for Gemini 3.1 Pro", "gemini-3.1-pro-preview", ThinkingLevelMap{ThinkingOff: nil, ThinkingMinimal: nil, ThinkingLow: new("low"), ThinkingMedium: new("medium"), ThinkingHigh: new("high"), ThinkingXHigh: nil, ThinkingMax: nil}, ThinkingMedium, "MEDIUM", true},
		// .upstream/v0.87.1/packages/ai/test/google-thinking-level-map.test.ts:178
		{"honors uppercase provider values for standard Google levels", "gemini-3.7-flash", ThinkingLevelMap{ThinkingHigh: new("LOW")}, ThinkingHigh, "LOW", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := &Model{ID: tc.id, ProviderMeta: ProviderMetadata{ProviderID: "test-google"}, Capabilities: ModelCapabilities{MaxThinking: ThinkingLevelHigh}, ThinkingLevelMap: tc.mapping}
			config, err := buildGeminiThinkingConfig(model, tc.level, true, nil)
			if err != nil {
				t.Fatal(err)
			}
			if config == nil || config.ThinkingLevel != tc.want {
				t.Fatalf("config=%#v want=%q", config, tc.want)
			}
			if tc.include && (config.IncludeThoughts == nil || !*config.IncludeThoughts) {
				t.Fatalf("includeThoughts=%v", config.IncludeThoughts)
			}
			if !tc.include && config.IncludeThoughts != nil {
				t.Fatalf("includeThoughts=%v, want omitted", config.IncludeThoughts)
			}
		})
	}
}

// google-shared.ts:85 toGoogleThinkingLevel maps each resolved level to Google's enum value; the disabled-thinking config of a
// level-based model sends that value (getDisabledGoogleThinkingConfig).
func TestToGoogleThinkingLevelUpstream(t *testing.T) {
	for level, want := range map[ResolvedGoogleThinkingLevel]GoogleThinkingLevel{
		ResolvedGoogleThinkingLevelMinimal: "MINIMAL",
		ResolvedGoogleThinkingLevelLow:     "LOW",
		ResolvedGoogleThinkingLevelMedium:  "MEDIUM",
		ResolvedGoogleThinkingLevelHigh:    "HIGH",
	} {
		if got := ToGoogleThinkingLevel(level); got != want {
			t.Errorf("ToGoogleThinkingLevel(%q) = %q, want %q", level, got, want)
		}
	}
	model := &Model{ID: "gemini-3.7-flash", ProviderMeta: ProviderMetadata{ProviderID: "test-google", Reasoning: true}, ThinkingLevelMap: ThinkingLevelMap{ThinkingOff: nil}}
	config, err := buildGeminiThinkingConfig(model, ThinkingOff, true, nil)
	if err != nil || config == nil || config.ThinkingLevel != "MINIMAL" {
		t.Fatalf("disabled config = %+v, %v", config, err)
	}
}

// google-shared.ts:98 toGoogleSdkThinkingLevel selects the @google/genai ThinkingLevel enum value of each Google API level through
// GOOGLE_SDK_THINKING_LEVEL_MAP (google-shared.ts:36-42); the request payload carries that enum value, which the level-based
// thinkingConfig of a Gemini 3 model and the disabled-thinking config both send (google-generative-ai.ts:406, google-shared.ts:110).
func TestToGoogleSdkThinkingLevelUpstream(t *testing.T) {
	for level, want := range map[GoogleThinkingLevel]GoogleSdkThinkingLevel{
		GoogleThinkingLevelUnspecified: "THINKING_LEVEL_UNSPECIFIED",
		GoogleThinkingLevelMinimal:     "MINIMAL",
		GoogleThinkingLevelLow:         "LOW",
		GoogleThinkingLevelMedium:      "MEDIUM",
		GoogleThinkingLevelHigh:        "HIGH",
	} {
		if got := ToGoogleSdkThinkingLevel(level); got != want {
			t.Errorf("ToGoogleSdkThinkingLevel(%q) = %q, want %q", level, got, want)
		}
	}
	for _, level := range []ResolvedGoogleThinkingLevel{ResolvedGoogleThinkingLevelMinimal, ResolvedGoogleThinkingLevelLow, ResolvedGoogleThinkingLevelMedium, ResolvedGoogleThinkingLevelHigh} {
		model := &Model{ID: "gemini-3.7-flash", ProviderMeta: ProviderMetadata{ProviderID: "test-google", Reasoning: true}}
		config, err := buildGeminiThinkingConfig(model, ModelThinkingLevel(level), true, nil)
		if err != nil || config == nil {
			t.Fatalf("level %q: config = %+v, %v", level, config, err)
		}
		if want := string(ToGoogleSdkThinkingLevel(ToGoogleThinkingLevel(level))); config.ThinkingLevel != want {
			t.Errorf("level %q: thinkingConfig.thinkingLevel = %q, want the SDK value %q", level, config.ThinkingLevel, want)
		}
	}
}
