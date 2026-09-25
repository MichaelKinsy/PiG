package codingagent

import (
	"fmt"
	"maps"
	"slices"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

func newModelThinkingSubmenu(settings Settings, models []*ai.Model, currentModel string, onChange func(*ai.Model, string), onDone func()) *SteppedSubmenu {
	overrides := maps.Clone(settings.ModelThinkingLevels)
	if overrides == nil {
		overrides = map[string]string{}
	}
	byKey := make(map[string]*ai.Model, len(models))
	for _, model := range models {
		byKey[modelSpec(model)] = model
	}
	defaultModel := settings.DefaultProvider + "/" + settings.DefaultModel
	globalDefault := settings.DefaultThinkingLevel
	if globalDefault == "" {
		globalDefault = DefaultThinkingLevel
	}
	steps := []SteppedSubmenuStep{
		{
			Key:         "model",
			Title:       func(map[string]string) string { return "Per-Model Thinking Level" },
			Description: func(map[string]string) string { return "Select a model to configure" },
			Options: func(map[string]string) []tui.SelectItem {
				sorted := slices.Clone(models)
				collator := collate.New(language.Und)
				slices.SortStableFunc(sorted, func(a, b *ai.Model) int {
					aKey, bKey := modelSpec(a), modelSpec(b)
					if aKey == currentModel {
						return -1
					}
					if bKey == currentModel {
						return 1
					}
					if aKey == defaultModel {
						return -1
					}
					if bKey == defaultModel {
						return 1
					}
					return collator.CompareString(a.ProviderMeta.ProviderID, b.ProviderMeta.ProviderID)
				})
				items := make([]tui.SelectItem, 0, len(sorted))
				for _, model := range sorted {
					key := modelSpec(model)
					items = append(items, tui.SelectItem{Value: key, Label: model.ID + " " + tui.ActiveTheme().Muted + "[" + model.ProviderMeta.ProviderID + "]\x1b[39m", Description: overrides[key]})
				}
				if len(items) == 0 {
					items = append(items, tui.SelectItem{Value: "__none__", Label: "No models available", Description: "Log in to a provider or configure an API key first"})
				}
				return items
			},
			Preselect: func(map[string]string) string {
				if currentModel != "" {
					return currentModel
				}
				return defaultModel
			},
			// upstream: packages/coding-agent/src/modes/interactive/components/settings-selector.ts:MODEL_PICKER_LAYOUT
			Layout: tui.SelectSubmenuOptions{Searchable: true, MinPrimaryColumnWidth: 12, MaxPrimaryColumnWidth: 46},
		},
		{
			Key: "level",
			Title: func(ctx map[string]string) string {
				if model := byKey[ctx["model"]]; model != nil {
					return fmt.Sprintf("Thinking Level for %s [%s]", model.ID, model.ProviderMeta.ProviderID)
				}
				return "Thinking Level for " + ctx["model"]
			},
			Description: func(map[string]string) string { return "Select default thinking level for this model" },
			Options: func(ctx map[string]string) []tui.SelectItem {
				model := byKey[ctx["model"]]
				if model == nil {
					return nil
				}
				levels := levelsForModel(model)
				items := make([]tui.SelectItem, 0, len(levels)+1)
				for _, level := range levels {
					label := "  " + level
					if level == overrides[ctx["model"]] {
						label = "✓ " + level
					}
					items = append(items, tui.SelectItem{Value: level, Label: label, Description: thinkingDescriptions[level]})
				}
				if _, exists := overrides[ctx["model"]]; exists {
					items = append(items, tui.SelectItem{Value: modelThinkingClearOverrideValue, Label: "  (clear override)", Description: "Revert to global default (" + globalDefault + ")"})
				}
				return items
			},
			Preselect: func(ctx map[string]string) string { return overrides[ctx["model"]] },
		},
	}
	return NewSteppedSubmenu(steps, func(ctx map[string]string) {
		model := byKey[ctx["model"]]
		if model == nil {
			return
		}
		level := ctx["level"]
		onChange(model, level)
		if level == modelThinkingClearOverrideValue {
			delete(overrides, ctx["model"])
		} else {
			overrides[ctx["model"]] = level
		}
	}, onDone, SteppedSubmenuOptions{Loop: true})
}

func (m *InteractiveMode) modelThinkingSettingsSubmenu(_ string, done func(*string)) tui.Component {
	var models []*ai.Model
	if m.opts.ModelRegistry != nil {
		for _, entry := range m.opts.ModelRegistry.GetAvailable() {
			generated := ai.GeneratedModel{Provider: entry.ProviderID, ID: entry.ModelID, Reasoning: entry.Reasoning, ThinkingLevelMap: entry.ThinkingLevelMap}
			models = append(models, generated.ToModel())
		}
	}
	menu := newModelThinkingSubmenu(m.opts.SettingsManager.Get(), models, modelSpec(m.opts.Model), func(model *ai.Model, level string) {
		var err error
		if level == modelThinkingClearOverrideValue {
			err = m.opts.SettingsManager.RemoveModelThinkingLevel(model.ProviderMeta.ProviderID, model.ID)
		} else {
			err = m.opts.SettingsManager.SetModelThinkingLevel(model.ProviderMeta.ProviderID, model.ID, level)
		}
		if err != nil {
			m.showError(err.Error())
			return
		}
		if modelSpec(model) == modelSpec(m.opts.Model) {
			if level == modelThinkingClearOverrideValue {
				level = m.opts.SettingsManager.GetDefaultThinkingLevel()
				if level == "" {
					level = DefaultThinkingLevel
				}
			}
			if err := m.applyThinkingLevel(string(ai.ClampThinkingLevel(m.opts.Model, ai.ThinkingLevel(level))), false); err != nil {
				m.showError(err.Error())
			}
		}
	}, func() {
		summary := "none"
		if count := len(m.opts.SettingsManager.Get().ModelThinkingLevels); count != 0 {
			summary = fmt.Sprintf("%d configured", count)
		}
		done(&summary)
	})
	return menu
}
