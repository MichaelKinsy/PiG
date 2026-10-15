package codingagent

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// upstream: packages/coding-agent/test/model-resolver.test.ts (d7f7e0dfe) referenceModel, referenceCatalog, referenceCases.
func referenceCatalog() []RuntimeModel {
	var models []RuntimeModel
	for _, entry := range [][2]string{
		{"anthropic", "claude-sonnet-4-5"},
		{"anthropic", "claude-sonnet-4-5-20250929"},
		{"anthropic", "claude-opus-4-8"},
		{"anthropic", "exact"},
		// Fuzzy-matches "shared"; an ambiguous exact "shared" must not fall through to it.
		{"anthropic", "shared-pro"},
		{"openai", "gpt-4o"},
		{"openrouter", "qwen/qwen3-coder:exacto"},
		{"openrouter", "openai/gpt-4o:extended"},
		{"gw-a", "shared"},
		{"gw-a", "anthropic/future"},
		{"gw-a", "anthropic/exact"},
		{"gw-a", "anthropic/claude-opus-4.8"},
		{"gw-a", "anthropic/claude-opus"},
		{"gw-b", "shared"},
		{"gw-b", "anthropic/future"},
		{"gw-b", "claude-opus-4-8"},
		{"gw-b", "gw-b/free"},
		{"gw-b", "zz:free"},
	} {
		models = append(models, RuntimeModel{Provider: entry[0], ID: entry[1], Name: entry[1], Reasoning: true})
	}
	return models
}

func TestResolveModelReferenceMatchesUpstream(t *testing.T) {
	type expected struct {
		model, thinking, warning string
		ambiguous                []string
		usableCount              int
		notFound                 bool
	}
	notFound := expected{notFound: true}
	for _, tc := range []struct {
		name     string
		text     string
		want     expected
		usable   []string
		provider string
		suffix   ThinkingSuffixMode
		explicit bool
		noFuzzy  bool
	}{
		// Reference syntax
		{name: "exact id", text: "claude-sonnet-4-5", want: expected{model: "anthropic/claude-sonnet-4-5"}},
		{name: "exact id is case-insensitive", text: "GPT-4O", want: expected{model: "openai/gpt-4o"}},
		{name: "fuzzy prefers aliases over dated ids", text: "sonnet", want: expected{model: "anthropic/claude-sonnet-4-5"}},
		{name: "thinking suffix", text: "sonnet:high", want: expected{model: "anthropic/claude-sonnet-4-5", thinking: "high"}},
		{name: "colon in a literal id is not a thinking suffix", text: "qwen/qwen3-coder:exacto", want: expected{model: "openrouter/qwen/qwen3-coder:exacto"}},
		{name: "provider prefix, colon id, and thinking suffix", text: "openrouter/qwen/qwen3-coder:exacto:high", want: expected{model: "openrouter/qwen/qwen3-coder:exacto", thinking: "high"}},
		{name: "raw id wins when the provider prefix names no matching model", text: "openai/gpt-4o:extended", want: expected{model: "openrouter/openai/gpt-4o:extended"}},
		{name: "fuzzy searches the prefixed provider first", text: "openrouter/qwen", want: expected{model: "openrouter/qwen/qwen3-coder:exacto"}},
		{name: "strict keeps an invalid suffix in the id", text: "gpt-4o:random", want: notFound},
		{name: "lenient drops an invalid suffix with a warning", text: "gpt-4o:random", suffix: ThinkingSuffixLenient, explicit: true, want: expected{model: "openai/gpt-4o", warning: `Invalid thinking level "random"`}},
		{name: "lenient invalid outer suffix discards the inner thinking level", text: "qwen/qwen3-coder:exacto:high:random", suffix: ThinkingSuffixLenient, explicit: true, want: expected{model: "openrouter/qwen/qwen3-coder:exacto", warning: `Invalid thinking level "random"`}},
		{name: "none keeps the suffix in the id", text: "gpt-4o:high", suffix: ThinkingSuffixNone, explicit: true, want: notFound},
		{name: "exact-only skips fuzzy matches", text: "sonnet", noFuzzy: true, want: notFound},
		{name: "empty text", text: "  ", want: notFound},

		// Exact matches across providers
		// #7327
		{name: "sole usable provider wins", text: "shared", usable: []string{"gw-b"}, want: expected{model: "gw-b/shared"}},
		{name: "no usable provider is ambiguous instead of falling through to fuzzy", text: "shared", want: expected{ambiguous: []string{"gw-a/shared", "gw-b/shared"}, usableCount: 0}},
		{name: "several usable providers are ambiguous", text: "shared", usable: []string{"gw-a", "gw-b"}, want: expected{ambiguous: []string{"gw-a/shared", "gw-b/shared"}, usableCount: 2}},
		{name: "thinking suffix does not change provider selection", text: "shared:high", usable: []string{"gw-b"}, want: expected{model: "gw-b/shared", thinking: "high"}},
		{name: "thinking suffix does not bypass ambiguity", text: "shared:high", usable: []string{"gw-a", "gw-b"}, want: expected{ambiguous: []string{"gw-a/shared", "gw-b/shared"}, usableCount: 2}},
		// #10160
		{name: "provider prefix without a direct model selects the usable raw id", text: "anthropic/future", usable: []string{"gw-b"}, want: expected{model: "gw-b/anthropic/future"}},
		// #5823
		{name: "usable qualified match beats a usable raw id", text: "anthropic/exact", usable: []string{"anthropic", "gw-a"}, want: expected{model: "anthropic/exact"}},
		// #5643
		{name: "usable raw id beats an unusable qualified match", text: "anthropic/exact", usable: []string{"gw-a"}, want: expected{model: "gw-a/anthropic/exact"}},
		{name: "unusable qualified match is kept when nothing is usable", text: "anthropic/exact", want: expected{model: "anthropic/exact"}},
		// Like `mistral/codestral` -> mistral/codestral-latest despite gateway id `mistral/codestral`
		{name: "named provider's usable fuzzy match beats a raw id elsewhere", text: "anthropic/claude-opus", usable: []string{"anthropic"}, want: expected{model: "anthropic/claude-opus-4-8"}},
		{name: "usable raw id beats the named provider's unusable fuzzy match", text: "anthropic/claude-opus", usable: []string{"gw-a"}, want: expected{model: "gw-a/anthropic/claude-opus"}},
		{name: "named provider's fuzzy match is kept when nothing is usable", text: "anthropic/claude-opus", want: expected{model: "anthropic/claude-opus-4-8"}},
		{name: "named provider's literal id beats its fuzzy matches", text: "gw-b/free", want: expected{model: "gw-b/gw-b/free"}},

		// Fuzzy matches across providers
		{name: "fuzzy prefers usable providers", text: "opus", usable: []string{"gw-a"}, want: expected{model: "gw-a/anthropic/claude-opus-4.8"}},
		{name: "fuzzy id tie across usable providers is ambiguous", text: "opus", usable: []string{"anthropic", "gw-a", "gw-b"}, want: expected{ambiguous: []string{"anthropic/claude-opus-4-8", "gw-b/claude-opus-4-8"}, usableCount: 2}},
		{name: "fuzzy id tie resolves to the sole usable provider", text: "opus", usable: []string{"anthropic"}, want: expected{model: "anthropic/claude-opus-4-8"}},

		// Explicit provider constraint
		{name: "explicit provider ignores usable providers elsewhere", text: "shared", provider: "gw-a", usable: []string{"gw-b"}, want: expected{model: "gw-a/shared"}},
		{name: "explicit provider accepts its own prefix", text: "GW-B/shared", provider: "gw-b", want: expected{model: "gw-b/shared"}},
		{name: "explicit provider allows fuzzy matches", text: "4o", provider: "openai", want: expected{model: "openai/gpt-4o"}},
		{name: "explicit provider never leaves the provider", text: "gpt-4o", provider: "gw-a", want: notFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			suffix := ThinkingSuffixStrict
			if tc.explicit {
				suffix = tc.suffix
			}
			request := ModelReferenceRequest{Text: tc.text, Provider: tc.provider, ThinkingSuffix: suffix, Fuzzy: !tc.noFuzzy}
			usable := func(provider string) bool { return slices.Contains(tc.usable, provider) }
			// Catalog order must never decide the result.
			catalog := referenceCatalog()
			reversed := slices.Clone(catalog)
			slices.Reverse(reversed)
			for _, models := range [][]RuntimeModel{catalog, reversed} {
				got := ResolveModelReference(request, models, usable)
				switch {
				case tc.want.notFound:
					if got.Status != ModelReferenceNotFound {
						t.Fatalf("status = %v, want not-found", got.Status)
					}
				case tc.want.ambiguous != nil:
					if got.Status != ModelReferenceAmbiguous {
						t.Fatalf("status = %v, want ambiguous", got.Status)
					}
					var refs []string
					for _, model := range got.Candidates {
						refs = append(refs, modelRef(model))
					}
					slices.Sort(refs)
					if !reflect.DeepEqual(refs, tc.want.ambiguous) || got.UsableCount != tc.want.usableCount {
						t.Fatalf("candidates = %v usableCount = %d, want %v %d", refs, got.UsableCount, tc.want.ambiguous, tc.want.usableCount)
					}
				default:
					if got.Status != ModelReferenceResolved {
						t.Fatalf("status = %v, want resolved", got.Status)
					}
					if modelRef(*got.Model) != tc.want.model || got.ThinkingLevel != tc.want.thinking {
						t.Fatalf("model = %s thinking = %q, want %s %q", modelRef(*got.Model), got.ThinkingLevel, tc.want.model, tc.want.thinking)
					}
					if tc.want.warning != "" && !strings.Contains(got.Warning, tc.want.warning) || tc.want.warning == "" && got.Warning != "" {
						t.Fatalf("warning = %q, want containing %q", got.Warning, tc.want.warning)
					}
				}
			}
		})
	}
}

// upstream: model-resolver.test.ts resolveModelScopeWithDiagnostics "adds every provider for a pattern matching the same id on several providers, with a warning".
func TestResolveModelScopeAmbiguousProvidersMatchesUpstream(t *testing.T) {
	models := []RuntimeModel{
		{Provider: "anthropic", ID: "claude-sonnet-4-5", Name: "Claude Sonnet 4.5"},
		{Provider: "openai", ID: "gpt-4o", Name: "GPT-4o"},
		{Provider: "openrouter", ID: "qwen/qwen3-coder:exacto", Name: "Qwen3 Coder Exacto"},
		{Provider: "openrouter", ID: "openai/gpt-4o:extended", Name: "GPT-4o Extended"},
		{Provider: "azure", ID: "gpt-4o", Name: "GPT-4o"},
		{Provider: "openai-codex", ID: "gpt-4o-mini", Name: "GPT-4o"},
	}
	for _, tc := range []struct{ pattern, thinking string }{{"gpt-4o:high", "high"}, {"GPT-4o", ""}} {
		t.Run(tc.pattern, func(t *testing.T) {
			got := ResolveModelScopeFromModels([]string{tc.pattern}, models)
			var scoped []string
			for _, entry := range got.ScopedModels {
				scoped = append(scoped, modelRef(entry.Model)+":"+entry.ThinkingLevel)
			}
			slices.Sort(scoped)
			if want := []string{"azure/gpt-4o:" + tc.thinking, "openai/gpt-4o:" + tc.thinking}; !reflect.DeepEqual(scoped, want) {
				t.Fatalf("scoped = %v, want %v", scoped, want)
			}
			want := []ModelScopeDiagnostic{{Type: "warning", Code: "ambiguous", Message: `Pattern "` + tc.pattern + `" matches several providers: azure, openai. Use provider/id to choose one.`, Pattern: tc.pattern}}
			if !reflect.DeepEqual(got.Diagnostics, want) {
				t.Fatalf("diagnostics = %+v, want %+v", got.Diagnostics, want)
			}
		})
	}
}

// upstream: model-resolver.test.ts resolveCliModel "duplicate raw ids behind a known provider prefix (#10160)".
type referenceCLIRuntime struct {
	models     []RuntimeModel
	configured []string
}

func (r referenceCLIRuntime) GetModels(string) []RuntimeModel { return r.models }
func (r referenceCLIRuntime) HasConfiguredAuth(provider string) bool {
	return slices.Contains(r.configured, provider)
}

func TestResolveCliModelDuplicateRawIDsMatchesUpstream(t *testing.T) {
	models := []RuntimeModel{
		{Provider: "anthropic", ID: "claude-sonnet-4-5", Name: "Claude Sonnet 4.5"},
		{Provider: "openrouter", ID: "anthropic/claude-opus-5.5", Name: "Claude Opus 5.5"},
		{Provider: "vercel-ai-gateway", ID: "anthropic/claude-opus-5.5", Name: "Claude Opus 5.5"},
	}
	t.Run("selects the authenticated gateway, with or without a thinking suffix", func(t *testing.T) {
		for _, tc := range []struct{ model, thinking string }{{"anthropic/claude-opus-5.5", ""}, {"anthropic/claude-opus-5.5:high", "high"}} {
			got := ResolveCliModel("", tc.model, "", false, referenceCLIRuntime{models, []string{"vercel-ai-gateway"}})
			if got.Error != "" || got.Model == nil || got.Model.Provider != "vercel-ai-gateway" || got.ThinkingLevel != ai.ThinkingLevel(tc.thinking) {
				t.Fatalf("%s: result = %+v", tc.model, got)
			}
		}
	})
	t.Run("reports ambiguity instead of choosing by catalog order", func(t *testing.T) {
		got := ResolveCliModel("", "anthropic/claude-opus-5.5", "", false, referenceCLIRuntime{models: models})
		if got.Model != nil || !strings.Contains(got.Error, "openrouter/anthropic/claude-opus-5.5, vercel-ai-gateway/anthropic/claude-opus-5.5") ||
			!strings.Contains(got.Error, "No matching provider is authenticated. Use --provider or provider/model.") {
			t.Fatalf("result = %+v", got)
		}
	})
	t.Run("does not let stored credentials choose the provider for --api-key", func(t *testing.T) {
		got := ResolveCliModel("", "anthropic/claude-opus-5.5", "", true, referenceCLIRuntime{models, []string{"vercel-ai-gateway"}})
		if got.Model != nil || !strings.Contains(got.Error, "--api-key does not choose a provider.") {
			t.Fatalf("result = %+v", got)
		}
	})
}
