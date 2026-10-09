package ai

import (
	"reflect"
	"testing"
)

func TestAdjustMaxTokensForThinking(t *testing.T) {
	cases := []struct {
		name                  string
		base, model           int
		level                 string
		wantMax, wantThinking int
	}{
		{"base preserved without legacy 32k cap", 64000, 128000, "off", 64000, 0},
		{"off", 32000, 128000, "off", 32000, 0},
		{"medium level", 32000, 128000, "medium", 40192, 8192},
		{"high level", 32000, 128000, "high", 48384, 16384},
		{"xhigh clamped to high", 32000, 128000, "xhigh", 48384, 16384},
		{"small model caps both", 4000, 8000, "high", 8000, 6976},
		{"very small model", 512, 1500, "high", 1500, 476},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotMax, gotThinking := AdjustMaxTokensForThinking(&tc.base, tc.model, tc.level, nil)
			if gotMax != tc.wantMax {
				t.Errorf("maxTokens = %d, want %d", gotMax, tc.wantMax)
			}
			if gotThinking != tc.wantThinking {
				t.Errorf("thinkingBudget = %d, want %d", gotThinking, tc.wantThinking)
			}
		})
	}
}

func TestAdjustMaxTokensForThinking_UnsetBaseUsesModelCap(t *testing.T) {
	gotMax, gotThinking := AdjustMaxTokensForThinking(nil, 128000, string(ThinkingHigh), nil)
	if gotMax != 128000 {
		t.Fatalf("maxTokens = %d, want 128000", gotMax)
	}
	if gotThinking != 16384 {
		t.Fatalf("thinkingBudget = %d, want 16384", gotThinking)
	}
}

func TestSimpleOptionsNoLegacyDefaultMaxTokensInjection(t *testing.T) {
	if _, got := any(DefaultThinkingBudgets()).(ThinkingBudgets); !got {
		t.Fatal("DefaultThinkingBudgets should return ThinkingBudgets")
	}
}

func TestTransportConstants(t *testing.T) {
	if TransportWebSocketCached != "websocket-cached" {
		t.Fatalf("TransportWebSocketCached = %q, want websocket-cached", TransportWebSocketCached)
	}
	if TransportAuto != "auto" {
		t.Fatalf("TransportAuto = %q, want auto", TransportAuto)
	}
	if API("xiaomi") != "xiaomi" {
		t.Fatalf("API(xiaomi) = %q, want xiaomi", API("xiaomi"))
	}
}

// packages/ai/src/api/simple-options.ts buildBaseOptions: maxTokens = clampMaxTokensToContext(model, context, options?.maxTokens ?? model.maxTokens); samplingParams = resolveSamplingParams(model, options?.reasoning ?? "off", options?.samplingParams); apiKey = apiKey || options?.apiKey; every other field is carried.
func TestBuildBaseOptions(t *testing.T) {
	model := &Model{
		Capabilities:                  ModelCapabilities{ContextWindow: 8192, MaxOutputTokens: 100_000},
		SamplingParams:                SamplingParams{"temperature": 0.5},
		SamplingParamsByThinkingLevel: SamplingParamsByThinkingLevel{"off": {"top_k": 4}, "high": {"top_k": 9}},
		ProviderMeta:                  ProviderMetadata{Reasoning: true},
	}
	empty := NormalizeContext(Context{})
	cases := []struct {
		name    string
		options StreamOptions
		apiKey  string
		check   func(t *testing.T, got StreamOptions)
	}{
		{"unset maxTokens is the model maximum, clamped to the window", StreamOptions{}, "", func(t *testing.T, got StreamOptions) {
			if want := 8192 - contextSafetyTokens; got.MaxTokens != want {
				t.Errorf("MaxTokens = %d, want %d", got.MaxTokens, want)
			}
		}},
		{"a requested budget that fits is kept", StreamOptions{MaxTokens: 1000}, "", func(t *testing.T, got StreamOptions) {
			if got.MaxTokens != 1000 {
				t.Errorf("MaxTokens = %d, want 1000", got.MaxTokens)
			}
		}},
		{"unset reasoning resolves the off level's sampling parameters", StreamOptions{}, "", func(t *testing.T, got StreamOptions) {
			if want := (SamplingParams{"temperature": 0.5, "top_k": 4}); !reflect.DeepEqual(got.SamplingParams, want) {
				t.Errorf("SamplingParams = %v, want %v", got.SamplingParams, want)
			}
		}},
		{"the reasoning level selects its own sampling parameters and the request wins", StreamOptions{Thinking: ThinkingLevel(ThinkingHigh), SamplingParams: SamplingParams{"temperature": 0.1}}, "", func(t *testing.T, got StreamOptions) {
			if want := (SamplingParams{"temperature": 0.1, "top_k": 9}); !reflect.DeepEqual(got.SamplingParams, want) {
				t.Errorf("SamplingParams = %v, want %v", got.SamplingParams, want)
			}
		}},
		{"apiKey replaces the option's key", StreamOptions{APIKey: "from-options"}, "from-caller", func(t *testing.T, got StreamOptions) {
			if got.APIKey != "from-caller" {
				t.Errorf("APIKey = %q", got.APIKey)
			}
		}},
		{"an empty apiKey keeps the option's key", StreamOptions{APIKey: "from-options"}, "", func(t *testing.T, got StreamOptions) {
			if got.APIKey != "from-options" {
				t.Errorf("APIKey = %q", got.APIKey)
			}
		}},
		{"other fields are carried", StreamOptions{SessionID: "s1", Headers: map[string]*string{}, Thinking: ThinkingLevel(ThinkingHigh)}, "", func(t *testing.T, got StreamOptions) {
			if got.SessionID != "s1" || got.Headers == nil || got.Thinking != ThinkingLevel(ThinkingHigh) {
				t.Errorf("options = %+v", got)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := tc.options
			tc.check(t, BuildBaseOptions(model, empty, tc.options, tc.apiKey))
			if !reflect.DeepEqual(before, tc.options) {
				t.Error("the caller's options were modified")
			}
		})
	}
}
