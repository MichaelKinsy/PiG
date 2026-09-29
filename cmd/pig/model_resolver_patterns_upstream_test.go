package main

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func upstreamResolverModels() []codingagent.RuntimeModel {
	return []codingagent.RuntimeModel{
		{Provider: "anthropic", ID: "claude-sonnet-4-5", Name: "Claude Sonnet 4.5", Reasoning: true},
		{Provider: "openai", ID: "gpt-4o", Name: "GPT-4o"},
		{Provider: "openrouter", ID: "qwen/qwen3-coder:exacto", Name: "Qwen3 Coder Exacto", Reasoning: true},
		{Provider: "openrouter", ID: "openai/gpt-4o:extended", Name: "GPT-4o Extended"},
	}
}

func TestModelResolverPatternsUpstream(t *testing.T) {
	type patternCase struct {
		name, pattern, provider, id, thinking, invalid string
		anyModel                                       bool
	}
	cases := []patternCase{
		// .upstream/v0.87.1/packages/coding-agent/test/model-resolver.test.ts:84
		{name: "exact match returns model with undefined thinking level", pattern: "claude-sonnet-4-5", id: "claude-sonnet-4-5"},
		// .upstream/v0.87.1/packages/coding-agent/test/model-resolver.test.ts:91
		{name: "partial match returns best model with undefined thinking level", pattern: "sonnet", id: "claude-sonnet-4-5"},
		// .upstream/v0.87.1/packages/coding-agent/test/model-resolver.test.ts:98
		{name: "no match returns undefined model and thinking level", pattern: "nonexistent"},
		// .upstream/v0.87.1/packages/coding-agent/test/model-resolver.test.ts:107
		{name: "sonnet:high returns sonnet with high thinking level", pattern: "sonnet:high", id: "claude-sonnet-4-5", thinking: "high"},
		// .upstream/v0.87.1/packages/coding-agent/test/model-resolver.test.ts:114
		{name: "gpt-4o:medium returns gpt-4o with medium thinking level", pattern: "gpt-4o:medium", id: "gpt-4o", thinking: "medium"},
		// .upstream/v0.87.1/packages/coding-agent/test/model-resolver.test.ts:132
		{name: "sonnet:random returns sonnet with undefined thinking level and warning", pattern: "sonnet:random", id: "claude-sonnet-4-5", invalid: "random"},
		// .upstream/v0.87.1/packages/coding-agent/test/model-resolver.test.ts:140
		{name: "gpt-4o:invalid returns gpt-4o with undefined thinking level and warning", pattern: "gpt-4o:invalid", id: "gpt-4o", invalid: "invalid"},
		// .upstream/v0.87.1/packages/coding-agent/test/model-resolver.test.ts:149
		{name: "qwen3-coder:exacto matches the model with undefined thinking level", pattern: "qwen/qwen3-coder:exacto", id: "qwen/qwen3-coder:exacto"},
		// .upstream/v0.87.1/packages/coding-agent/test/model-resolver.test.ts:156
		{name: "openrouter/qwen/qwen3-coder:exacto matches with provider prefix", pattern: "openrouter/qwen/qwen3-coder:exacto", provider: "openrouter", id: "qwen/qwen3-coder:exacto"},
		// .upstream/v0.87.1/packages/coding-agent/test/model-resolver.test.ts:164
		{name: "qwen3-coder:exacto:high matches model with high thinking level", pattern: "qwen/qwen3-coder:exacto:high", id: "qwen/qwen3-coder:exacto", thinking: "high"},
		// .upstream/v0.87.1/packages/coding-agent/test/model-resolver.test.ts:171
		{name: "openrouter/qwen/qwen3-coder:exacto:high matches with provider and thinking level", pattern: "openrouter/qwen/qwen3-coder:exacto:high", provider: "openrouter", id: "qwen/qwen3-coder:exacto", thinking: "high"},
		// .upstream/v0.87.1/packages/coding-agent/test/model-resolver.test.ts:179
		{name: "gpt-4o:extended matches the extended model with undefined thinking level", pattern: "openai/gpt-4o:extended", id: "openai/gpt-4o:extended"},
		// .upstream/v0.87.1/packages/coding-agent/test/model-resolver.test.ts:188
		{name: "qwen3-coder:exacto:random returns model with undefined thinking level and warning", pattern: "qwen/qwen3-coder:exacto:random", id: "qwen/qwen3-coder:exacto", invalid: "random"},
		// .upstream/v0.87.1/packages/coding-agent/test/model-resolver.test.ts:196
		{name: "qwen3-coder:exacto:high:random returns model with undefined thinking level and warning", pattern: "qwen/qwen3-coder:exacto:high:random", id: "qwen/qwen3-coder:exacto", invalid: "random"},
		// .upstream/v0.87.1/packages/coding-agent/test/model-resolver.test.ts:206
		{name: "empty pattern matches via partial matching", anyModel: true},
		// .upstream/v0.87.1/packages/coding-agent/test/model-resolver.test.ts:213
		{name: "pattern ending with colon treats empty suffix as invalid", pattern: "sonnet:", id: "claude-sonnet-4-5", invalid: "<empty>"},
	}
	// .upstream/v0.87.1/packages/coding-agent/test/model-resolver.test.ts:121
	for _, level := range []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"} {
		cases = append(cases, patternCase{name: "all valid thinking levels work/" + level, pattern: "sonnet:" + level, id: "claude-sonnet-4-5", thinking: level})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseModelPattern(tc.pattern, upstreamResolverModels(), true)
			if tc.id == "" && !tc.anyModel {
				if got.Model != nil {
					t.Fatalf("model=%+v, want nil", got.Model)
				}
			} else if got.Model == nil || (!tc.anyModel && got.Model.ID != tc.id) || (tc.provider != "" && got.Model.Provider != tc.provider) {
				t.Fatalf("model=%+v", got.Model)
			}
			if got.ThinkingLevel != tc.thinking {
				t.Errorf("thinking=%q, want %q", got.ThinkingLevel, tc.thinking)
			}
			warning := ""
			if tc.invalid != "" {
				suffix := tc.invalid
				if suffix == "<empty>" {
					suffix = ""
				}
				warning = fmt.Sprintf(`Invalid thinking level "%s" in pattern "%s". Using default instead.`, suffix, tc.pattern)
			}
			if got.Warning != warning {
				t.Errorf("warning=%q, want %q", got.Warning, warning)
			}
		})
	}
}

func TestModelResolverScopeUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/model-resolver.test.ts:224
	t.Run("returns scoped models and structured diagnostics without writing console warnings", func(t *testing.T) {
		var got codingagent.ResolveModelScopeResult
		stderr := captureStderr(t, func() {
			got = codingagent.ResolveModelScopeFromModels([]string{"sonnet:high", "gpt-4o:invalid", "missing"}, upstreamResolverModels())
		})
		if stderr != "" {
			t.Fatalf("unexpected diagnostic output=%q", stderr)
		}
		if len(got.ScopedModels) != 2 || got.ScopedModels[0].Model.ID != "claude-sonnet-4-5" || got.ScopedModels[0].ThinkingLevel != "high" || got.ScopedModels[1].Model.ID != "gpt-4o" || got.ScopedModels[1].ThinkingLevel != "" {
			t.Fatalf("scope=%+v", got.ScopedModels)
		}
		want := []codingagent.ModelScopeDiagnostic{{Type: "warning", Code: "invalid-thinking-level", Pattern: "gpt-4o:invalid", Message: `Invalid thinking level "invalid" in pattern "gpt-4o:invalid". Using default instead.`}, {Type: "warning", Code: "no-match", Pattern: "missing", Message: `No models match pattern "missing"`}}
		if !reflect.DeepEqual(got.Diagnostics, want) {
			t.Fatalf("diagnostics=%+v", got.Diagnostics)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/model-resolver.test.ts:256
	t.Run("resolveModelScope preserves CLI warning output", func(t *testing.T) {
		scoped, warnings := resolveModelScopeFromModels([]string{"missing"}, upstreamResolverModels())
		if len(scoped) != 0 || len(warnings) != 1 {
			t.Fatalf("scope=%v, warnings=%v", scoped, warnings)
		}
		stderr := captureStderr(t, func() {
			for _, warning := range warnings {
				printModelDiagnostic(warning)
			}
		})
		if !strings.Contains(stderr, `Warning: No models match pattern "missing"`) {
			t.Fatalf("stderr=%q", stderr)
		}
	})
	for _, tc := range []struct{ name, pattern, thinking string }{
		// .upstream/v0.87.1/packages/coding-agent/test/model-resolver.test.ts:273
		{"resolves bracketed model ids as exact references before glob matching", "custom/bracketed-model[1m]", ""},
		// .upstream/v0.87.1/packages/coding-agent/test/model-resolver.test.ts:296
		{"resolves bracketed model ids with thinking levels as exact references before glob matching", "custom/bracketed-model[1m]:high", "high"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			models := append(upstreamResolverModels(), codingagent.RuntimeModel{Provider: "custom", ID: "bracketed-model[1m]", Name: "Bracketed Model", Reasoning: true})
			got := codingagent.ResolveModelScopeFromModels([]string{tc.pattern}, models)
			if len(got.ScopedModels) != 1 || got.ScopedModels[0].Model.ID != "bracketed-model[1m]" || got.ScopedModels[0].ThinkingLevel != tc.thinking || len(got.Diagnostics) != 0 {
				t.Fatalf("result=%+v", got)
			}
		})
	}
}
