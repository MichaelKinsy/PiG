package cli

import (
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

// Pi: packages/coding-agent/src/core/model-resolver.ts:272 (ModelScopeDiagnostic.code); packages/coding-agent/src/core/model-resolver.ts:274 (ModelScopeDiagnostic.pattern); packages/coding-agent/src/core/model-resolver.ts:278 (ResolveModelScopeResult.scopedModels); packages/coding-agent/src/index.ts:223 (ModelScopeDiagnostic.type).
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
