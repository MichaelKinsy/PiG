// Ports packages/coding-agent/src/experimental/services/slash-commands-provider.ts.
package services

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/chord"
)

var thinkingDescriptions = map[ai.ThinkingLevel]string{
	"off": "No reasoning", "minimal": "Very brief reasoning", "low": "Light reasoning",
	"medium": "Moderate reasoning", "high": "Deep reasoning", "xhigh": "Extra-high reasoning", "max": "Maximum reasoning",
}

// BuiltInSlashCommandsOptions supplies the presentation generation reload. It waits for replacement and retired-generation disposal before returning.
type BuiltInSlashCommandsOptions struct {
	ReloadPresentationPlugins func(context.Context, chord.JsonValue) error
}

// CreateBuiltInSlashCommandsFacet installs model, thinking, compact and reload contributions, in that order. Activation and disposal run on the command registry's owner executor; command bodies may block and must run off the TUI input loop.
func CreateBuiltInSlashCommandsFacet(options BuiltInSlashCommandsOptions) chord.Facet {
	return chord.DefineFacet(chord.Facet{Id: "@pi/slash-commands-builtin", Setup: func(env *chord.FacetEnvironment) error {
		commands, err := chord.UseService(env, SlashCommandsDefinition)
		if err != nil {
			return err
		}
		models, err := chord.UseService(env, ModelsDefinition)
		if err != nil {
			return err
		}
		controller, err := chord.UseService(env, AgentControllerDefinition)
		if err != nil {
			return err
		}
		ui, err := chord.UseService(env, PresentationUIDefinition)
		if err != nil {
			return err
		}
		presentationPlugins, err := chord.UseService(env, PresentationPluginsDefinition)
		if err != nil {
			return err
		}
		sessionPlugins, err := chord.UseService(env, SessionPluginsDefinition)
		if err != nil {
			return err
		}
		return env.OnActivate(func(context.Context) error {
			commandService, err := commands.Get()
			if err != nil {
				return err
			}
			modelService, err := models.Get()
			if err != nil {
				return err
			}
			controllerService, err := controller.Get()
			if err != nil {
				return err
			}
			uiService, err := ui.Get()
			if err != nil {
				return err
			}
			presentationService, err := presentationPlugins.Get()
			if err != nil {
				return err
			}
			sessionService, err := sessionPlugins.Get()
			if err != nil {
				return err
			}
			contributions := []SlashCommandContribution{
				modelCommand(modelService, uiService), thinkingCommand(modelService, uiService), compactCommand(controllerService, uiService),
				{Name: "reload", Description: new("Reload server-selected plugins"), Run: func(ctx context.Context, _ string) (SlashCommandRunResult, error) {
					if err := uiService.ShowStatus(ctx, "Reloading plugins…"); err != nil {
						return nil, err
					}
					data, err := presentationService.Reload(ctx)
					if err != nil {
						return nil, err
					}
					if err := sessionService.Reload(ctx); err != nil {
						return nil, err
					}
					if err := options.ReloadPresentationPlugins(ctx, data); err != nil {
						return nil, err
					}
					return nil, uiService.ShowStatus(ctx, "Reloaded plugins.")
				}},
			}
			for _, command := range contributions {
				remove, err := commandService.Replace(command)
				if err != nil {
					return err
				}
				if err := env.Own(func(context.Context) error { remove(); return nil }); err != nil {
					remove()
					return err
				}
			}
			return nil
		})
	}})
}

func modelCommand(models Models, ui PresentationUI) SlashCommandContribution {
	return SlashCommandContribution{
		Name: "model", Description: new("Select model"), ArgumentHint: new("<provider/model>"),
		GetArgumentCompletions: func(prefix string) ([]SlashCommandCompletion, error) {
			normalized := strings.ToLower(prefix)
			result := []SlashCommandCompletion{}
			if state := models.State().Value(); state != nil {
				for _, model := range state.Catalog.AvailableModels {
					if strings.Contains(strings.ToLower(model.Provider+"/"+model.ModelId+" "+model.Name), normalized) {
						result = append(result, SlashCommandCompletion{Value: model.Provider + "/" + model.ModelId, Label: model.ModelId, Description: new(model.Provider)})
					}
				}
			}
			return result, nil
		},
		Run: func(ctx context.Context, args string) (SlashCommandRunResult, error) {
			state := models.State().Value()
			if state == nil {
				return nil, errors.New("Models service is not ready")
			}
			selected := exactModel(state.Catalog.AvailableModels, args)
			if args != "" && selected == nil {
				return nil, fmt.Errorf("Unknown model: %s", args)
			}
			if selected == nil {
				items := make([]PresentationSelectItem, 0, len(state.Catalog.AvailableModels))
				for _, model := range state.Catalog.AvailableModels {
					label := model.Name
					if current := state.Configuration.Model; current != nil && *current == model.ModelRef {
						label += " (selected)"
					}
					items = append(items, PresentationSelectItem{Value: model.Provider + "/" + model.ModelId, Label: label, Description: new(model.Provider + "/" + model.ModelId)})
				}
				var selectedValue *string
				if model := state.Configuration.Model; model != nil {
					selectedValue = new(model.Provider + "/" + model.ModelId)
				}
				value, err := ui.Select(ctx, "Select model:", items, selectedValue)
				if err != nil || value == nil {
					return nil, err
				}
				selected = exactModel(state.Catalog.AvailableModels, *value)
				if selected == nil {
					return nil, fmt.Errorf("Unknown model: %s", *value)
				}
			}
			if err := models.Select(ctx, selected.ModelRef); err != nil {
				return nil, err
			}
			return nil, ui.ShowStatus(ctx, "Selected "+selected.Provider+"/"+selected.ModelId+".")
		},
	}
}

func thinkingCommand(models Models, ui PresentationUI) SlashCommandContribution {
	return SlashCommandContribution{
		Name: "thinking", Description: new("Set thinking level"), ArgumentHint: new("<level>"),
		Run: func(ctx context.Context, args string) (SlashCommandRunResult, error) {
			levels, err := models.GetThinkingLevels(ctx)
			if err != nil {
				return nil, err
			}
			selected := ai.ThinkingLevel(strings.ToLower(args))
			found := slices.Contains(levels, selected)
			if args != "" && !found {
				names := make([]string, len(levels))
				for i, level := range levels {
					names[i] = string(level)
				}
				return nil, fmt.Errorf("Unknown thinking level \"%s\". Available levels: %s.", args, strings.Join(names, ", "))
			}
			if !found {
				items := make([]PresentationSelectItem, 0, len(levels))
				for _, level := range levels {
					label := string(level)
					if state := models.State().Value(); state != nil && state.Configuration.ThinkingLevel == level {
						label += " (selected)"
					}
					var description *string
					if text, ok := thinkingDescriptions[level]; ok {
						description = new(text)
					}
					items = append(items, PresentationSelectItem{Value: string(level), Label: label, Description: description})
				}
				var selectedValue *string
				if state := models.State().Value(); state != nil {
					selectedValue = new(string(state.Configuration.ThinkingLevel))
				}
				value, err := ui.Select(ctx, "Select thinking level:", items, selectedValue)
				if err != nil || value == nil {
					return nil, err
				}
				selected = ai.ThinkingLevel(*value)
				if !slices.Contains(levels, selected) {
					return nil, fmt.Errorf("Unknown thinking level: %s", *value)
				}
			}
			if err := models.SelectThinking(ctx, selected); err != nil {
				return nil, err
			}
			return nil, ui.ShowStatus(ctx, "Thinking level: "+string(selected)+".")
		},
	}
}

func compactCommand(controller AgentController, ui PresentationUI) SlashCommandContribution {
	return SlashCommandContribution{Name: "compact", Description: new("Manually compact the session context"), ArgumentHint: new("<instructions>"), Run: func(ctx context.Context, args string) (SlashCommandRunResult, error) {
		if err := ui.ShowStatus(ctx, "Compacting…"); err != nil {
			return nil, err
		}
		var instructions *string
		if args != "" {
			instructions = new(args)
		}
		return controller.Compact(ctx, AgentCompactionRequest{CustomInstructions: instructions})
	}}
}

func exactModel(models []ModelSummary, query string) *ModelSummary {
	if query == "" {
		return nil
	}
	normalized := strings.ToLower(query)
	var selected *ModelSummary
	for _, model := range models {
		if strings.ToLower(model.Provider+"/"+model.ModelId) == normalized || strings.ToLower(model.ModelId) == normalized {
			if selected != nil {
				return nil
			}
			selected = new(model)
		}
	}
	return selected
}
