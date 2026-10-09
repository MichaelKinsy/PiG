package services

import (
	"context"
	"reflect"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/chord"
)

func selectorState(t *testing.T, thinking ai.ModelThinkingLevel) *chord.MutableReplicatedState[*ModelsState] {
	t.Helper()
	state, err := chord.NewReplicatedState(&ModelsState{Catalog: ModelsCatalog{AvailableModels: []ModelSummary{
		{ModelRef: ModelRef{Provider: "custom", ModelId: "reasoner"}, Name: "Deep Thinker", Reasoning: true},
		{ModelRef: ModelRef{Provider: "key", ModelId: "fast"}, Name: "Fast One"},
	}}, Configuration: ModelsConfiguration{Model: &ModelRef{Provider: "custom", ModelId: "reasoner"}, ThinkingLevel: thinking}})
	requireModelsOK(t, err)
	return state
}

// upstream: slash-commands-provider.ts:151-199. Completions match the lower-cased "provider/modelId name" text; the selector lists every available model with the selected one marked and the configured model preselected; a selector value that names no model is an error.
func TestBuiltinModelCommandCompletionsAndSelector(t *testing.T) {
	models := &presentationModelsRecorder{state: selectorState(t, "off")}
	ui := &presentationUIRecorder{}
	command := modelCommand(models, ui)
	for query, want := range map[string][]SlashCommandCompletion{
		"thinker": {{Value: "custom/reasoner", Label: "reasoner", Description: new("custom")}},
		"KEY/":    {{Value: "key/fast", Label: "fast", Description: new("key")}},
		"zzz":     {},
		"":        {{Value: "custom/reasoner", Label: "reasoner", Description: new("custom")}, {Value: "key/fast", Label: "fast", Description: new("key")}},
	} {
		got, err := command.GetArgumentCompletions(query)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("completions(%q) = %#v, %v; want %#v", query, got, err, want)
		}
	}
	ui.selectValue = new("key/fast")
	if result, err := command.Run(t.Context(), ""); err != nil || result != nil {
		t.Fatalf("selector run = %#v, %v", result, err)
	}
	wantItems := []PresentationSelectItem{
		{Value: "custom/reasoner", Label: "Deep Thinker (selected)", Description: new("custom/reasoner")},
		{Value: "key/fast", Label: "Fast One", Description: new("key/fast")},
	}
	if ui.selectedTitle != "Select model:" || !reflect.DeepEqual(ui.selectedItems, wantItems) || ui.selectedValue == nil || *ui.selectedValue != "custom/reasoner" {
		t.Fatalf("selector = %q %#v %v", ui.selectedTitle, ui.selectedItems, ui.selectedValue)
	}
	if models.selected == nil || *models.selected != (ModelRef{Provider: "key", ModelId: "fast"}) || !reflect.DeepEqual(ui.statuses, []string{"Selected key/fast."}) {
		t.Fatalf("selection = %v, statuses %v", models.selected, ui.statuses)
	}
	models.selected, ui.statuses, ui.selectValue = nil, nil, new("nope/none")
	if _, err := command.Run(t.Context(), ""); err == nil || err.Error() != "Unknown model: nope/none" {
		t.Fatalf("unknown selector value = %v", err)
	}
	if models.selected != nil || len(ui.statuses) != 0 {
		t.Fatalf("unknown selector value changed state: %v %v", models.selected, ui.statuses)
	}
}

// upstream: slash-commands-provider.ts:201-232. The selector lists the available levels with a description each and the configured one marked and preselected; a selector value outside the levels is an error; success reports "Thinking level: <level>.".
func TestBuiltinThinkingCommandSelector(t *testing.T) {
	levels := []ai.ModelThinkingLevel{"off", "minimal", "low", "medium", "high", "xhigh", "max"}
	models := &presentationModelsRecorder{state: selectorState(t, "medium"), levels: levels}
	ui := &presentationUIRecorder{selectValue: new("xhigh")}
	command := thinkingCommand(models, ui)
	if result, err := command.Run(t.Context(), ""); err != nil || result != nil {
		t.Fatalf("selector run = %#v, %v", result, err)
	}
	descriptions := []string{"No reasoning", "Very brief reasoning", "Light reasoning", "Moderate reasoning", "Deep reasoning", "Extra-high reasoning", "Maximum reasoning"}
	var wantItems []PresentationSelectItem
	for i, level := range levels {
		label := string(level)
		if level == "medium" {
			label += " (selected)"
		}
		wantItems = append(wantItems, PresentationSelectItem{Value: string(level), Label: label, Description: new(descriptions[i])})
	}
	if ui.selectedTitle != "Select thinking level:" || !reflect.DeepEqual(ui.selectedItems, wantItems) || ui.selectedValue == nil || *ui.selectedValue != "medium" {
		t.Fatalf("selector = %q %#v %v", ui.selectedTitle, ui.selectedItems, ui.selectedValue)
	}
	if models.thinking == nil || *models.thinking != "xhigh" || !reflect.DeepEqual(ui.statuses, []string{"Thinking level: xhigh."}) {
		t.Fatalf("selection = %v, statuses %v", models.thinking, ui.statuses)
	}
	models.thinking, ui.statuses, ui.selectValue = nil, nil, new("ultra")
	if _, err := command.Run(t.Context(), ""); err == nil || err.Error() != "Unknown thinking level: ultra" {
		t.Fatalf("unknown selector value = %v", err)
	}
	if models.thinking != nil || len(ui.statuses) != 0 {
		t.Fatalf("unknown selector value changed state: %v %v", models.thinking, ui.statuses)
	}
	// A level the model does not support has no description entry.
	models.levels = []ai.ModelThinkingLevel{"off", "custom"}
	ui.selectValue = nil
	if result, err := command.Run(t.Context(), ""); err != nil || result != nil {
		t.Fatalf("cancelled selector = %#v, %v", result, err)
	}
	if got := ui.selectedItems[1]; got.Description != nil || got.Value != "custom" {
		t.Fatalf("unlisted level item = %#v", got)
	}
}

// compactOrderController records the statuses already shown when Compact starts.
type compactOrderController struct {
	presentationControllerRecorder
	ui                *presentationUIRecorder
	statusesAtCompact []string
	calls             int
}

func (controller *compactOrderController) Compact(ctx context.Context, request AgentCompactionRequest) (AgentOperationResponse, error) {
	controller.calls++
	controller.statusesAtCompact = slices.Clone(controller.ui.statuses)
	return controller.presentationControllerRecorder.Compact(ctx, request)
}

// upstream: slash-commands-provider.ts:234-244. Compact shows "Compacting…" before it calls the controller, passes empty arguments as null instructions, and returns the controller's response.
func TestBuiltinCompactAnnouncesBeforeCompacting(t *testing.T) {
	ui := &presentationUIRecorder{}
	controller := &compactOrderController{ui: ui}
	result, err := compactCommand(controller, ui).Run(t.Context(), "tighten")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(controller.statusesAtCompact, []string{"Compacting…"}) || controller.instructions == nil || *controller.instructions != "tighten" {
		t.Fatalf("statuses at compact %v instructions %v", controller.statusesAtCompact, controller.instructions)
	}
	if response, ok := result.(AgentOperationResponse); !ok || !response.Accepted || response.OperationID == nil || *response.OperationID != "compact-id" {
		t.Fatalf("result = %#v", result)
	}
	if _, err := compactCommand(controller, ui).Run(t.Context(), ""); err != nil || controller.instructions != nil {
		t.Fatalf("empty arguments = %v, instructions %v", err, controller.instructions)
	}
}
