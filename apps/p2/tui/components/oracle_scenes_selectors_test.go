package components

import (
	"fmt"
	"strings"
	"testing"
)

func catalogModels() []SelectorModel {
	models := []SelectorModel{
		{Provider: "anthropic", ModelID: "claude-sonnet-5", Name: "Claude Sonnet 5"},
		{Provider: "anthropic", ModelID: "claude-opus-5-long-identifier-for-truncation", Name: "Claude Opus 5 with a long display name"},
		{Provider: "openai", ModelID: "gpt-6", Name: "GPT 6"},
		{Provider: "openai", ModelID: "gpt-6-mini", Name: "GPT 6 mini"},
		{Provider: "google", ModelID: "gemini-4", Name: "Gemini 日本語"},
		{Provider: "faux", ModelID: "faux-1", Name: "Faux"},
	}
	for i := range 12 {
		models = append(models, SelectorModel{Provider: "bulk", ModelID: fmt.Sprintf("m%d", i), Name: fmt.Sprintf("Bulk model %d", i)})
	}
	return models
}

func init() {
	noop := func() {}
	registerScene("dynamic-border", func(t testing.TB, ctx oracleContext) oracleScene {
		return oracleScene{component: NewDynamicBorder(ctx, "borderAccent")}
	})
	registerScene("secret-input", func(t testing.TB, ctx oracleContext) oracleScene {
		return oracleScene{
			component: NewSecretInput(ctx), focus: new(true),
			inputs: []string{"a", "b", "日", "\x7f", "\x1b[200~se\ncret\x1b[201~", strings.Repeat("x", 40), "\x15", "p", "\x1b[200~par", "tial", "\x1b[201~", "q"},
		}
	})
	registerScene("secret-input-unfocused", func(t testing.TB, ctx oracleContext) oracleScene {
		return oracleScene{component: NewSecretInput(ctx), focus: new(false), inputs: []string{"abc"}}
	})
	registerScene("model-selector", func(t testing.TB, ctx oracleContext) oracleScene {
		models := catalogModels()
		return oracleScene{
			component: NewModelSelector(ctx, ModelSelectorOptions{
				Models: models, Scoped: []SelectorModel{models[0], models[2], models[4]},
				Current: &ModelRef{Provider: "openai", ModelID: "gpt-6"}, DefaultModel: &ModelRef{Provider: "anthropic", ModelID: "claude-sonnet-5"},
				Title: "Select model", OnSelect: func(SelectorModel) {}, OnSelectAsDefault: func(SelectorModel) {}, OnCancel: noop,
			}),
			focus:  new(true),
			inputs: []string{keyDownArrow, keyDownArrow, "\t", keyDownArrow, "\x13", "g", "p", "t", "\x7f", keyPageDown, keyUpArrow, keyEnter, keyEsc},
		}
	})
	registerScene("model-selector-unscoped", func(t testing.TB, ctx oracleContext) oracleScene {
		return oracleScene{
			component: NewModelSelector(ctx, ModelSelectorOptions{Models: catalogModels(), Current: &ModelRef{Provider: "faux", ModelID: "faux-1"}, InitialSearch: "gem", OnSelect: func(SelectorModel) {}, OnCancel: noop}),
			focus:     new(true),
			inputs:    []string{"\t", keyDownArrow, "x", "y", "z", keyUpArrow, "\x15"},
		}
	})
	registerScene("model-selector-empty", func(t testing.TB, ctx oracleContext) oracleScene {
		return oracleScene{
			component: NewModelSelector(ctx, ModelSelectorOptions{OnSelect: func(SelectorModel) {}, OnCancel: noop}),
			focus:     new(false), inputs: []string{keyDownArrow},
		}
	})
	levels := []string{"off", "minimal", "low", "medium", "high", "xhigh"}
	registerScene("thinking-selector", func(t testing.TB, ctx oracleContext) oracleScene {
		return oracleScene{
			component: NewThinkingSelector(ctx, ThinkingSelectorOptions{Current: "low", Levels: levels, DefaultLevel: "medium", OnSelect: func(string) {}, OnCancel: noop, OnSelectAsDefault: func(string) {}}),
			focus:     new(true),
			inputs:    []string{keyDownArrow, keyDownArrow, "\x13", "h", "i", "\x7f", keyUpArrow, keyEnter, keyEsc},
		}
	})
	registerScene("thinking-selector-plain", func(t testing.TB, ctx oracleContext) oracleScene {
		return oracleScene{
			component: NewThinkingSelector(ctx, ThinkingSelectorOptions{Current: "high", Levels: []string{"off", "high"}, OnSelect: func(string) {}, OnCancel: noop}),
			focus:     new(false), inputs: []string{keyDownArrow, "q"},
		}
	})
	registerScene("scoped-models", func(t testing.TB, ctx oracleContext) oracleScene {
		return oracleScene{
			component: NewScopedModelsSelector(ctx, ScopedModelsSelectorOptions{
				Models: catalogModels(), Enabled: []ModelRef{{Provider: "openai", ModelID: "gpt-6"}, {Provider: "bulk", ModelID: "m3"}},
				OnSave: func([]string) {}, OnCancel: noop,
			}),
			focus:  new(true),
			inputs: []string{keyDownArrow, " ", keyDownArrow, keyEnter, "b", "u", "\x7f", keyPageDown, "\x13", keyEsc},
		}
	})
	registerScene("scoped-models-empty", func(t testing.TB, ctx oracleContext) oracleScene {
		return oracleScene{
			component: NewScopedModelsSelector(ctx, ScopedModelsSelectorOptions{OnSave: func([]string) {}, OnCancel: noop}),
			focus:     new(false), inputs: []string{keyDownArrow, " "},
		}
	})
}
