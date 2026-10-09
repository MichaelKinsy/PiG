package services

// pi: packages/coding-agent/src/experimental/services/presentation-ui.ts

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/chord"
)

type presentationUIRecorder struct {
	statuses      []string
	selectValue   *string
	selectedTitle string
	selectedItems []PresentationSelectItem
	selectedValue *string
	failure       error
}

func (ui *presentationUIRecorder) Select(_ context.Context, title string, items []PresentationSelectItem, selected *string) (*string, error) {
	ui.selectedTitle, ui.selectedItems, ui.selectedValue = title, items, selected
	return ui.selectValue, ui.failure
}
func (ui *presentationUIRecorder) ShowStatus(_ context.Context, status string) error {
	ui.statuses = append(ui.statuses, status)
	return ui.failure
}

// upstream: packages/coding-agent/src/experimental/services/presentation-ui.ts:9-18
func TestPresentationUILocalViewFollowsReloadAndRevocation(t *testing.T) {
	t.Parallel()
	first := &presentationUIRecorder{selectValue: new("first")}
	second := &presentationUIRecorder{selectValue: new("")}
	provide := func(ui PresentationUI) chord.Facet {
		return chord.Facet{Id: "ui", Setup: func(env *chord.FacetEnvironment) error {
			return chord.ProvideService(env, PresentationUIDefinition, ui)
		}}
	}
	var retained PresentationUI
	consumer := chord.Facet{Id: "consumer", Setup: func(env *chord.FacetEnvironment) error {
		ref, err := chord.UseService(env, PresentationUIDefinition)
		if err != nil {
			return err
		}
		return env.OnActivate(func(context.Context) error { var err error; retained, err = ref.Get(); return err })
	}}
	host, err := chord.CreateFacetHost(t.Context(), chord.FacetOptions{Facets: []chord.Facet{provide(first), consumer}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := host.Dispose(context.Background()); err != nil {
			t.Error(err)
		}
	})
	if got := host.Services().Catalogue(); len(got) != 0 {
		t.Fatalf("local UI leaked to remote catalogue: %#v", got)
	}
	items := []PresentationSelectItem{{Value: "", Label: "empty", Description: new("")}}
	if err := retained.ShowStatus(t.Context(), "before"); err != nil {
		t.Fatal(err)
	}
	value, err := retained.Select(t.Context(), "first-title", items, nil)
	if err != nil || value == nil || *value != "first" {
		t.Fatalf("first select = %v, %v", value, err)
	}
	if err := host.Reload(t.Context(), []chord.Facet{provide(second)}); err != nil {
		t.Fatal(err)
	}
	value, err = retained.Select(t.Context(), "replacement", items, new(""))
	if err != nil || value == nil || *value != "" {
		t.Fatalf("empty selection became cancellation: %v, %v", value, err)
	}
	if second.selectedValue == nil || *second.selectedValue != "" || !reflect.DeepEqual(second.selectedItems, items) || second.selectedTitle != "replacement" {
		t.Fatalf("selector inputs changed: %#v", second)
	}
	second.selectValue = nil
	value, err = retained.Select(t.Context(), "cancel", nil, nil)
	if err != nil || value != nil {
		t.Fatalf("cancellation = %v, %v", value, err)
	}
	failure := errors.New("selector failure")
	second.failure = failure
	if _, err := retained.Select(t.Context(), "fail", nil, nil); !errors.Is(err, failure) {
		t.Fatalf("select error = %v, want %v", err, failure)
	}
	if err := retained.ShowStatus(t.Context(), "after"); !errors.Is(err, failure) {
		t.Fatalf("status error = %v, want %v", err, failure)
	}
	if !reflect.DeepEqual(first.statuses, []string{"before"}) || !reflect.DeepEqual(second.statuses, []string{"after"}) {
		t.Fatalf("retained view did not follow cutover: %#v / %#v", first.statuses, second.statuses)
	}
	if err := host.Dispose(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := retained.ShowStatus(t.Context(), "revoked"); err == nil {
		t.Fatal("disposed facet UI remained usable")
	}
	if _, err := retained.Select(t.Context(), "revoked", nil, nil); err == nil {
		t.Fatal("disposed facet selector remained usable")
	}
}

// upstream: packages/coding-agent/src/experimental/services/slash-commands-provider.ts:246-255
func TestBuiltinExactModelRejectsAmbiguousBareIDs(t *testing.T) {
	t.Parallel()
	models := []ModelSummary{
		{ModelRef: ModelRef{Provider: "oauth", ModelId: "shared"}, Name: "A"},
		{ModelRef: ModelRef{Provider: "api-key", ModelId: "shared"}, Name: "B"},
		{ModelRef: ModelRef{Provider: "custom", ModelId: "solo"}, Name: "C"},
	}
	for _, row := range []struct {
		query string
		want  *ModelSummary
	}{
		{"", nil}, {"shared", nil}, {"unknown", nil}, {"OAuth/SHARED", &models[0]}, {"API-KEY/shared", &models[1]}, {"SOLO", &models[2]}, {" solo ", nil},
	} {
		t.Run(row.query, func(t *testing.T) {
			if got := exactModel(models, row.query); !reflect.DeepEqual(got, row.want) {
				t.Fatalf("exactModel(%q) = %#v, want %#v", row.query, got, row.want)
			}
		})
	}
}

type presentationModelsRecorder struct {
	Models
	state    *chord.MutableReplicatedState[*ModelsState]
	levels   []ai.ModelThinkingLevel
	selected *ModelRef
	thinking *ai.ModelThinkingLevel
	failure  error
}

type presentationControllerRecorder struct {
	AgentController
	instructions *string
}

func (controller *presentationControllerRecorder) Compact(_ context.Context, request AgentCompactionRequest) (AgentOperationResponse, error) {
	controller.instructions = request.CustomInstructions
	return AgentOperationResponse{Accepted: true, OperationID: new("compact-id")}, nil
}

type presentationReloadRecorder struct {
	PresentationPlugins
	calls   *[]string
	failure error
}

func (plugins presentationReloadRecorder) Reload(context.Context) (chord.JsonValue, error) {
	*plugins.calls = append(*plugins.calls, "presentation")
	return map[string]any{"generation": "candidate"}, plugins.failure
}

type sessionReloadRecorder struct {
	calls   *[]string
	failure error
}

func (plugins sessionReloadRecorder) Reload(context.Context) error {
	*plugins.calls = append(*plugins.calls, "session")
	return plugins.failure
}

// upstream: packages/coding-agent/src/experimental/services/slash-commands-provider.ts:116-149,234-244
func TestBuiltinFacetReloadStopsAtFirstFailure(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name   string
		failAt string
		calls  []string
	}{
		{"success", "", []string{"presentation", "session", "local"}},
		{"presentation failure", "presentation", []string{"presentation"}},
		{"session failure", "session", []string{"presentation", "session"}},
		{"local failure", "local", []string{"presentation", "session", "local"}},
	} {
		t.Run(row.name, func(t *testing.T) {
			var calls []string
			failure := errors.New(row.name)
			failureAt := func(stage string) error {
				if row.failAt == stage {
					return failure
				}
				return nil
			}
			state, err := chord.NewReplicatedState(&ModelsState{Catalog: ModelsCatalog{AvailableModels: []ModelSummary{}}, Configuration: ModelsConfiguration{ThinkingLevel: "off"}})
			if err != nil {
				t.Fatal(err)
			}
			models := &presentationModelsRecorder{state: state}
			ui := &presentationUIRecorder{}
			controller := &presentationControllerRecorder{}
			registry := NewSlashCommandRegistry()
			provide := chord.Facet{Id: "dependencies", Setup: func(env *chord.FacetEnvironment) error {
				if err := chord.ProvideService[Models](env, ModelsDefinition, models); err != nil {
					return err
				}
				if err := chord.ProvideService[PresentationUI](env, PresentationUIDefinition, ui); err != nil {
					return err
				}
				if err := chord.ProvideService[AgentController](env, AgentControllerDefinition, controller); err != nil {
					return err
				}
				if err := chord.ProvideService[PresentationPlugins](env, PresentationPluginsDefinition, presentationReloadRecorder{calls: &calls, failure: failureAt("presentation")}); err != nil {
					return err
				}
				return chord.ProvideService[SessionPlugins](env, SessionPluginsDefinition, sessionReloadRecorder{calls: &calls, failure: failureAt("session")})
			}}
			host, err := chord.CreateFacetHost(t.Context(), chord.FacetOptions{Facets: []chord.Facet{
				CreateSlashCommandsRuntimeFacet(registry), provide,
				CreateBuiltInSlashCommandsFacet(BuiltInSlashCommandsOptions{ReloadPresentationPlugins: func(_ context.Context, data chord.JsonValue) error {
					if !reflect.DeepEqual(data, map[string]any{"generation": "candidate"}) {
						t.Errorf("local reload data = %#v", data)
					}
					calls = append(calls, "local")
					return failureAt("local")
				}}),
			}})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := host.Dispose(context.Background()); err != nil {
					t.Error(err)
				}
			})
			commands := registry.List()
			if got, want := slashCommandNames(commands), []string{"model", "thinking", "compact", "reload"}; !reflect.DeepEqual(got, want) {
				t.Fatalf("command order = %#v, want %#v", got, want)
			}
			result, err := commands[3].Run(t.Context(), "ignored")
			if result != nil || (row.failAt == "" && err != nil) || (row.failAt != "" && (err == nil || err.Error() != row.name)) {
				t.Fatalf("reload = %#v, %v", result, err)
			}
			if !reflect.DeepEqual(calls, row.calls) {
				t.Fatalf("reload order = %#v, want %#v", calls, row.calls)
			}
			statuses := []string{"Reloading plugins…"}
			if row.failAt == "" {
				statuses = append(statuses, "Reloaded plugins.")
			}
			if !reflect.DeepEqual(ui.statuses, statuses) {
				t.Fatalf("reload statuses = %#v, want %#v", ui.statuses, statuses)
			}
			for _, args := range []string{"", " preserve whitespace "} {
				result, err := commands[2].Run(t.Context(), args)
				if err != nil {
					t.Fatal(err)
				}
				operation, ok := result.(AgentOperationResponse)
				if !ok || !operation.Accepted || operation.OperationID == nil || *operation.OperationID != "compact-id" {
					t.Fatalf("compact result = %#v", result)
				}
				var want *string
				if args != "" {
					want = new(args)
				}
				if !reflect.DeepEqual(controller.instructions, want) {
					t.Fatalf("compact instructions = %v, want %v", controller.instructions, want)
				}
			}
			if err := host.Dispose(t.Context()); err != nil {
				t.Fatal(err)
			}
			if got := registry.List(); len(got) != 0 {
				t.Fatalf("disposed facet retained commands: %#v", got)
			}
		})
	}
}

func (models *presentationModelsRecorder) State() chord.ReplicatedStateOf[*ModelsState] {
	return models.state
}
func (models *presentationModelsRecorder) GetThinkingLevels(context.Context) ([]ai.ModelThinkingLevel, error) {
	return models.levels, models.failure
}
func (models *presentationModelsRecorder) Select(_ context.Context, model ModelRef) error {
	models.selected = new(model)
	return models.failure
}
func (models *presentationModelsRecorder) SelectThinking(_ context.Context, level ai.ModelThinkingLevel) error {
	models.thinking = new(level)
	return models.failure
}

// upstream: packages/coding-agent/src/experimental/services/slash-commands-provider.ts:151-232
func TestBuiltinCommandsPreserveArgumentsAndCancellation(t *testing.T) {
	t.Parallel()
	state, err := chord.NewReplicatedState(&ModelsState{Catalog: ModelsCatalog{AvailableModels: []ModelSummary{
		{ModelRef: ModelRef{Provider: "custom", ModelId: "reasoner"}, Name: "Reasoner", Reasoning: true},
		{ModelRef: ModelRef{Provider: "key", ModelId: "fast"}, Name: "Fast"},
	}}, Configuration: ModelsConfiguration{Model: &ModelRef{Provider: "custom", ModelId: "reasoner"}, ThinkingLevel: "off"}})
	if err != nil {
		t.Fatal(err)
	}
	models := &presentationModelsRecorder{state: state, levels: []ai.ModelThinkingLevel{"off", "high"}}
	ui := &presentationUIRecorder{}
	command := modelCommand(models, ui)
	got, err := command.GetArgumentCompletions("REASON")
	want := []SlashCommandCompletion{{Value: "custom/reasoner", Label: "reasoner", Description: new("custom")}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("completions = %#v, %v; want %#v", got, err, want)
	}
	if result, err := command.Run(t.Context(), ""); err != nil || result != nil {
		t.Fatalf("cancelled command = %#v, %v", result, err)
	}
	if models.selected != nil || len(ui.statuses) != 0 {
		t.Fatalf("cancelled selection changed model/status: %#v / %#v", models.selected, ui.statuses)
	}
	if _, err := command.Run(t.Context(), "REASONER"); err != nil {
		t.Fatal(err)
	}
	if models.selected == nil || *models.selected != (ModelRef{Provider: "custom", ModelId: "reasoner"}) || !reflect.DeepEqual(ui.statuses, []string{"Selected custom/reasoner."}) {
		t.Fatalf("selected model/status = %#v / %#v", models.selected, ui.statuses)
	}
	thinking := thinkingCommand(models, ui)
	if _, err := thinking.Run(t.Context(), "HIGH"); err != nil {
		t.Fatal(err)
	}
	if models.thinking == nil || *models.thinking != "high" {
		t.Fatalf("thinking = %v, want high", models.thinking)
	}
	if _, err := thinking.Run(t.Context(), "nope"); err == nil || err.Error() != "Unknown thinking level \"nope\". Available levels: off, high." {
		t.Fatalf("unknown level error = %v", err)
	}
	if _, err := command.Run(t.Context(), "missing"); err == nil || err.Error() != "Unknown model: missing" {
		t.Fatalf("unknown model error = %v", err)
	}
}
