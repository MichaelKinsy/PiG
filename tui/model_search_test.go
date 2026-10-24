package tui

// pi: packages/coding-agent/src/modes/interactive/model-search.ts

import (
	"slices"
	"strings"
	"testing"
)

func TestGetModelSearchText(t *testing.T) {
	cases := []struct {
		item ModelSearchItem
		want string
	}{
		{ModelSearchItem{ID: "gpt-5", Provider: "openai"}, "gpt-5 openai openai/gpt-5 openai gpt-5"},
		{ModelSearchItem{ID: "gpt-5", Provider: "openai", Name: "GPT-5"}, "gpt-5 openai openai/gpt-5 openai gpt-5 GPT-5"},
		{ModelSearchItem{ID: "openai/gpt-5", Provider: "openrouter", Name: "OpenAI: GPT-5"}, "openai/gpt-5 openrouter openrouter/openai/gpt-5 openrouter openai/gpt-5 OpenAI: GPT-5"},
	}
	for _, tc := range cases {
		if got := GetModelSearchText(tc.item); got != tc.want {
			t.Errorf("GetModelSearchText(%+v) = %q, want %q", tc.item, got, tc.want)
		}
	}
}

func TestGetModelSelectorSearchText(t *testing.T) {
	cases := []struct {
		item ModelSearchItem
		want string
	}{
		// An empty name is upstream's falsy name: no trailing segment.
		{ModelSearchItem{ID: "gpt-5", Provider: "openai"}, "openai openai/gpt-5 openai gpt-5"},
		{ModelSearchItem{ID: "gpt-5", Provider: "openai", Name: "GPT-5"}, "openai openai/gpt-5 openai gpt-5 GPT-5"},
		{ModelSearchItem{ID: "openai/gpt-5", Provider: "openrouter", Name: "OpenAI: GPT-5"}, "openrouter openrouter/openai/gpt-5 openrouter openai/gpt-5 OpenAI: GPT-5"},
	}
	for _, tc := range cases {
		if got := GetModelSelectorSearchText(tc.item); got != tc.want {
			t.Errorf("GetModelSelectorSearchText(%+v) = %q, want %q", tc.item, got, tc.want)
		}
	}
}

// proxyProviderFixture mixes direct-provider models with proxy providers
// whose ids embed another provider's prefix.
func proxyProviderFixture() []ModelSelectorItem {
	return []ModelSelectorItem{
		{Provider: "openrouter", ID: "openai/gpt-5", Name: "OpenAI: GPT-5"},
		{Provider: "openrouter", ID: "openai/gpt-5-mini", Name: "OpenAI: GPT-5 Mini"},
		{Provider: "vercel-ai-gateway", ID: "openai/gpt-5", Name: "GPT-5"},
		{Provider: "openai", ID: "gpt-5", Name: "GPT-5"},
		{Provider: "openai", ID: "gpt-5-mini", Name: "GPT-5 Mini"},
	}
}

// The expected orders below are Pi 0.87.1 fuzzyFilter results over this exact
// fixture (packages/tui/src/fuzzy.ts with model-search.ts, evaluated under
// Node 24 against .upstream/current).
func TestModelSelectorSearchRanksProviderPrefixedQueryAheadOfProxyIDs(t *testing.T) {
	cases := []struct {
		query    string
		selector []string
		search   []string
	}{
		{
			query:    "openai/gpt-5",
			selector: []string{"openai/gpt-5", "openai/gpt-5-mini", "openrouter/openai/gpt-5", "openrouter/openai/gpt-5-mini", "vercel-ai-gateway/openai/gpt-5"},
			search:   []string{"openrouter/openai/gpt-5", "openrouter/openai/gpt-5-mini", "vercel-ai-gateway/openai/gpt-5", "openai/gpt-5", "openai/gpt-5-mini"},
		},
		{
			query:    "openai gpt-5",
			selector: []string{"openai/gpt-5", "openai/gpt-5-mini", "openrouter/openai/gpt-5", "openrouter/openai/gpt-5-mini", "vercel-ai-gateway/openai/gpt-5"},
			search:   []string{"openrouter/openai/gpt-5", "openrouter/openai/gpt-5-mini", "vercel-ai-gateway/openai/gpt-5", "openai/gpt-5", "openai/gpt-5-mini"},
		},
		{
			query:    "gpt-5",
			selector: []string{"openai/gpt-5", "openai/gpt-5-mini", "openrouter/openai/gpt-5", "openrouter/openai/gpt-5-mini", "vercel-ai-gateway/openai/gpt-5"},
			search:   []string{"openai/gpt-5", "openai/gpt-5-mini", "openrouter/openai/gpt-5", "openrouter/openai/gpt-5-mini", "vercel-ai-gateway/openai/gpt-5"},
		},
		{
			query:    "openrouter/openai/gpt-5",
			selector: []string{"openrouter/openai/gpt-5", "openrouter/openai/gpt-5-mini"},
			search:   []string{"openrouter/openai/gpt-5", "openrouter/openai/gpt-5-mini"},
		},
	}
	items := proxyProviderFixture()
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			rank := func(text func(ModelSearchItem) string) []string {
				filtered := FuzzyFilter(items, tc.query, func(item ModelSelectorItem) string {
					return text(ModelSearchItem{ID: item.ID, Provider: item.Provider, Name: item.Name})
				})
				out := make([]string, len(filtered))
				for i, item := range filtered {
					out[i] = item.FQ()
				}
				return out
			}
			if got := rank(GetModelSelectorSearchText); !slices.Equal(got, tc.selector) {
				t.Errorf("selector ranking = %v, want %v", got, tc.selector)
			}
			if got := rank(GetModelSearchText); !slices.Equal(got, tc.search) {
				t.Errorf("search ranking = %v, want %v", got, tc.search)
			}

			// The /model selector itself must use the selector ranking and
			// select its best match.
			ms := NewStaticModelSelectorComponent("Select model", nil, items, "")
			ms.SetFilter(tc.query)
			got := make([]string, len(ms.filtered))
			for i, idx := range ms.filtered {
				got[i] = ms.active[idx].FQ()
			}
			if !slices.Equal(got, tc.selector) {
				t.Fatalf("ModelSelectorComponent filtered = %v, want %v", got, tc.selector)
			}
			for _, line := range ms.Render(100) {
				plain := stripANSI(line)
				if strings.HasPrefix(plain, "→ ") {
					// Pi 0.87.1 rows: cursor column, current-model marker
					// column ("  " when not current), then id and provider.
					first := ms.active[ms.filtered[0]]
					if !strings.HasPrefix(plain, "→   "+first.ID+" ["+first.Provider+"]") {
						t.Fatalf("selected row = %q, want %s", plain, first.FQ())
					}
					return
				}
			}
			t.Fatal("selected row not rendered")
		})
	}
}

func TestModelSelectorSearchMatchesRawNameAndFooterFallsBackToID(t *testing.T) {
	items := []ModelSelectorItem{
		{Provider: "fixture", ID: "alpha", Name: "Needle Model"},
		{Provider: "fixture", ID: "beta"},
	}
	ms := NewStaticModelSelectorComponent("Select model", nil, items, "")
	ms.SetFilter("needle")
	if ms.VisibleCount() != 1 || ms.active[ms.filtered[0]].ID != "alpha" {
		t.Fatalf("name search visible=%d", ms.VisibleCount())
	}
	joined := stripANSI(strings.Join(ms.Render(80), "\n"))
	if !strings.Contains(joined, "Model Name: Needle Model") {
		t.Fatalf("footer missing raw name:\n%s", joined)
	}

	ms.SetFilter("beta")
	joined = stripANSI(strings.Join(ms.Render(80), "\n"))
	if !strings.Contains(joined, "Model Name: beta") {
		t.Fatalf("footer did not fall back to id:\n%s", joined)
	}
}

// Ports packages/coding-agent/test/model-search.test.ts (Pi #11220, #10065): ranking a query that a scattered alignment used to bury.
func TestModelSelectorSearchRanksContiguousMatchesAboveScatteredOnes(t *testing.T) {
	rank := func(items []ModelSearchItem, query string) []string {
		filtered := FuzzyFilter(items, query, GetModelSelectorSearchText)
		out := make([]string, len(filtered))
		for i, item := range filtered {
			out[i] = item.ID
		}
		return out
	}
	t.Run("ranks models containing the query above scattered matches", func(t *testing.T) {
		got := rank([]ModelSearchItem{
			{Provider: "openrouter", ID: "openai/gpt-oss-120b", Name: "OpenAI: gpt-oss-120b"},
			{Provider: "openrouter", ID: "anthropic/claude-sonnet-4.5", Name: "Anthropic: Claude Sonnet 4.5"},
			{Provider: "openrouter", ID: "anthropic/claude-opus-4.1", Name: "Anthropic: Claude Opus 4.1"},
			{Provider: "anthropic", ID: "claude-sonnet-4-5", Name: "Claude Sonnet 4.5"},
			{Provider: "anthropic", ID: "claude-opus-4-1", Name: "Claude Opus 4.1"},
		}, "opus")
		first := slices.Sorted(slices.Values(got[:2]))
		if want := []string{"anthropic/claude-opus-4.1", "claude-opus-4-1"}; !slices.Equal(first, want) {
			t.Fatalf("first two = %v, want %v", first, want)
		}
	})
	t.Run("ranks the typed model above models sharing a long provider prefix", func(t *testing.T) {
		got := rank([]ModelSearchItem{
			{Provider: "fireworks", ID: "accounts/fireworks/models/deepseek-v4-flash-0731"},
			{Provider: "fireworks", ID: "accounts/fireworks/models/kimi-k3"},
			{Provider: "fireworks", ID: "accounts/fireworks/routers/firerouter", Name: "FireRouter"},
		}, "firerouter")
		if got[0] != "accounts/fireworks/routers/firerouter" {
			t.Fatalf("ranking = %v, want the router first", got)
		}
	})
}

// #5916 (Pi 606af8662): packages/coding-agent/test/model-search.test.ts getDistinctModelName.
func TestGetDistinctModelName(t *testing.T) {
	hidden := []ModelSearchItem{
		{ID: "claude-sonnet-4-5", Name: "Claude Sonnet 4.5"},
		{ID: "openai/gpt-oss-120b", Name: "OpenAI: gpt-oss-120b"},
		{ID: "eu.anthropic.claude-sonnet-4-5-20250929-v1:0", Name: "Claude Sonnet 4.5 (EU)"},
		{ID: "gpt-5", Name: ""},
		{ID: "gpt-5"},
	}
	for _, item := range hidden {
		if got := GetDistinctModelName(item); got != "" {
			t.Errorf("GetDistinctModelName(%+v) = %q, want it hidden", item, got)
		}
	}
	shown := []ModelSearchItem{
		{ID: "minimax/minimax-m3", Name: "MiniMax M3 [rasmus@openrouter, parasail]"},
		{ID: "gemini-3.1-flash-lite-image", Name: "Nano Banana 2 Lite"},
		// Single-character words must match a whole ID token: "4" is not covered by "mistral-small-2603".
		{ID: "mistral-small-2603", Name: "Mistral Small 4"},
	}
	for _, item := range shown {
		if got := GetDistinctModelName(item); got != item.Name {
			t.Errorf("GetDistinctModelName(%+v) = %q, want %q", item, got, item.Name)
		}
	}
}
