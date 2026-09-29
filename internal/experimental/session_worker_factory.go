package experimental

// Ports packages/coding-agent/src/experimental/session-worker.ts (coding Harness factory and process entry).

import (
	"context"
	"errors"
	"slices"

	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/agentharness"
	envpkg "github.com/MichaelKinsy/PiG/agent/harness/env"
	hruntime "github.com/MichaelKinsy/PiG/agent/harness/runtime"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/agent/harness/tools"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/experimental/services"
)

// RunSessionWorkerProcess runs the real coding Harness factory through the worker's owned control and resource lifetime. The executable validates and consumes the internal role before calling it.
func RunSessionWorkerProcess(ctx context.Context, args []string) error {
	return RunSessionWorkerWithHarness(ctx, args, createCodingAgentHarness)
}

func createCodingAgentHarness(ctx context.Context, stored session.Session, options SessionWorkerOptions, executionEnv *envpkg.NodeExecutionEnv) (SessionWorkerRuntime, error) {
	collaborators, err := coding.NewServices(coding.ServicesOptions{CWD: stored.Metadata().Cwd})
	if err != nil {
		return SessionWorkerRuntime{}, err
	}
	modelRuntime := collaborators.ModelRuntime()
	// ModelRuntime.create awaits an offline refresh by default and leaves provider diagnostics in the runtime.
	_ = modelRuntime.Refresh(ctx, ai.ModelsRefreshOptions{AllowNetwork: new(false)})
	settings := collaborators.SettingsManager()
	selection := workerModelSelection{runtime: modelRuntime, hasConfiguredAuth: collaborators.Registry().HasConfiguredAuth}
	var model *codingagent.RuntimeModel
	var thinking string
	if options.Model == "" {
		resolved, err := codingagent.FindInitialModel(codingagent.FindInitialModelOptions{
			ModelRuntime: selection, IsContinuing: true,
			DefaultProvider: settings.GetDefaultProvider(), DefaultModelId: settings.GetDefaultModel(),
			DefaultThinkingLevel: settings.GetDefaultThinkingLevel(),
		})
		if err != nil {
			return SessionWorkerRuntime{}, err
		}
		model, thinking = resolved.Model, resolved.ThinkingLevel
	} else {
		resolved := codingagent.ResolveCliModel(options.Provider, options.Model, "", selection)
		if resolved.Error != "" {
			return SessionWorkerRuntime{}, errors.New("Session worker could not resolve model: " + resolved.Error)
		}
		model, thinking = resolved.Model, resolved.ThinkingLevel
	}
	if model == nil || model.NativeModel == nil {
		return SessionWorkerRuntime{}, errors.New("Session worker could not resolve a model")
	}
	selectedTools := []harness.AgentHarnessTool{*tools.CreateReadTool(nil), *tools.CreateWriteTool(), *tools.CreateBashTool(nil)}
	activeTools := make([]string, len(selectedTools))
	for index, tool := range selectedTools {
		activeTools[index] = tool.Name
	}
	created, err := hruntime.CreateAgentHarness(ctx, hruntime.AgentHarnessOptions{
		Session: stored, Models: workerHarnessModels{ModelRuntime: modelRuntime}, Model: model.NativeModel, ThinkingLevel: ai.ThinkingLevel(thinking),
		Tools: selectedTools, ActiveToolNames: activeTools, Resources: agentharness.Resources{},
		ToolContext: func(context.Context) (any, error) { return tools.ExecutionToolContext{Env: executionEnv}, nil },
	})
	if err != nil {
		return SessionWorkerRuntime{}, err
	}
	result, err := func() (SessionWorkerRuntime, error) {
		lane, err := created.Harness.Lane(ctx, "main", nil)
		if err != nil {
			return SessionWorkerRuntime{}, err
		}
		current, err := lane.GetActiveTools(ctx)
		if err != nil {
			return SessionWorkerRuntime{}, err
		}
		if !slices.Equal(current, activeTools) {
			if err := lane.SetActiveTools(ctx, activeTools); err != nil {
				return SessionWorkerRuntime{}, err
			}
		}
		loader, err := CreateSessionPluginFacetLoader(options.PluginManifestPaths)
		if err != nil {
			return SessionWorkerRuntime{}, err
		}
		return SessionWorkerRuntime{
			Harness: &codingWorkerHarness{harness: created.Harness}, Lane: codingWorkerLane{Lane: lane},
			ModelRuntime: workerModelsServiceRuntime{runtime: modelRuntime}, SettingsManager: settings, FacetLoader: loader,
		}, nil
	}()
	if err != nil {
		if cleanup := created.Harness.Close(ctx); cleanup != nil {
			return SessionWorkerRuntime{}, &services.AggregateError{Message: "Session worker model selection and cleanup failed", Errors: []any{err, cleanup}}
		}
	}
	return result, err
}

// workerModelSelection projects only resolver metadata while retaining every native model field through the non-wire attachment. Availability comes from the runtime's own snapshot.
type workerModelSelection struct {
	runtime           *coding.ModelRuntime
	hasConfiguredAuth func(string) bool
}

func workerModelMetadata(model *ai.Model) codingagent.RuntimeModel {
	return codingagent.RuntimeModel{NativeModel: model, Provider: model.ProviderMeta.ProviderID, ID: model.ID, Name: model.DisplayName, Reasoning: model.ProviderMeta.Reasoning, Headers: ai.ProviderHeadersFromStrings(model.ProviderMeta.Headers)}
}
func (view workerModelSelection) GetModels(provider string) []codingagent.RuntimeModel {
	result := []codingagent.RuntimeModel{}
	for _, model := range view.runtime.GetModels() {
		if provider == "" || model.ProviderMeta.ProviderID == provider {
			result = append(result, workerModelMetadata(model))
		}
	}
	return result
}
func (view workerModelSelection) HasConfiguredAuth(provider string) bool {
	return view.hasConfiguredAuth(provider)
}
func (view workerModelSelection) GetModel(provider, id string) *codingagent.RuntimeModel {
	model := view.runtime.GetModel(provider, id)
	if model == nil {
		return nil
	}
	return new(workerModelMetadata(model))
}
func (view workerModelSelection) GetAvailableSnapshot() []codingagent.RuntimeModel {
	models := view.runtime.GetAvailableSnapshot()
	result := make([]codingagent.RuntimeModel, len(models))
	for index, model := range models {
		result[index] = workerModelMetadata(model)
	}
	return result
}

// workerHarnessModels passes the Harness's optional request options to the Model Runtime, as Pi's ModelRuntime accepts them at model-runtime.ts:638-669.
type workerHarnessModels struct{ *coding.ModelRuntime }

func workerStreamOptions(options []ai.StreamOptions) ai.StreamOptions {
	if len(options) == 0 {
		return ai.StreamOptions{}
	}
	return options[0]
}
func (models workerHarnessModels) StreamSimple(ctx context.Context, model *ai.Model, request ai.Context, options ...ai.StreamOptions) *ai.AssistantMessageEventStream {
	return models.ModelRuntime.StreamSimple(ctx, model, request, workerStreamOptions(options))
}
func (models workerHarnessModels) CompleteSimple(ctx context.Context, model *ai.Model, request ai.Context, options ...ai.StreamOptions) *ai.AssistantMessage {
	return models.ModelRuntime.CompleteSimple(ctx, model, request, workerStreamOptions(options))
}

type workerModelsServiceRuntime struct{ runtime *coding.ModelRuntime }

func (view workerModelsServiceRuntime) GetAvailableSnapshot() []*ai.Model {
	return view.runtime.GetAvailableSnapshot()
}
func (view workerModelsServiceRuntime) GetModel(provider, id string) *ai.Model {
	return view.runtime.GetModel(provider, id)
}
func (view workerModelsServiceRuntime) Refresh(ctx context.Context) (services.ModelsRefreshResult, error) {
	result := view.runtime.Refresh(ctx)
	return services.ModelsRefreshResult{Aborted: result.Aborted, Errors: result.Errors}, nil
}

type codingWorkerHarness struct{ harness *hruntime.Harness }

func (worker *codingWorkerHarness) Events() *agentharness.HarnessEventBus {
	return worker.harness.Events()
}
func (worker *codingWorkerHarness) Lane(ctx context.Context, name string) (services.SessionWorkerServiceLane, error) {
	lane, err := worker.harness.Lane(ctx, name, nil)
	if err != nil {
		return nil, err
	}
	return codingWorkerLane{Lane: lane}, nil
}
func (worker *codingWorkerHarness) Close(ctx context.Context) error { return worker.harness.Close(ctx) }

// codingWorkerLane delegates every operation to the real public Lane method, including structural continuations. It does not reconstruct Accept/Drive or claim separate producer admission.
type codingWorkerLane struct{ *hruntime.Lane }

func workerOperationRecord(record session.OperationResultRecord) services.LaneOperation {
	result := services.LaneOperation{OperationID: record.OperationID, Status: record.Status}
	if record.Error != nil {
		result.Error = &services.AgentOperationError{Code: record.Error.Code, Message: record.Error.Message}
	}
	return result
}
func workerRunOperation(outcome agentharness.RunOutcome) services.LaneOperation {
	if outcome.Suspended != nil {
		return services.LaneOperation{OperationID: outcome.Suspended.OperationID, Status: outcome.Suspended.Status}
	}
	return workerOperationRecord(*outcome.Record)
}
func (lane codingWorkerLane) Prompt(ctx context.Context, message string, images []ai.ImageContent) (services.LaneOperation, error) {
	result, err := lane.Lane.Prompt(ctx, message, images)
	if err != nil {
		return services.LaneOperation{}, err
	}
	return workerRunOperation(result), nil
}
func (lane codingWorkerLane) RequestAbort(ctx context.Context, id string) error {
	_, err := lane.Lane.RequestAbort(ctx, id)
	return err
}
func (lane codingWorkerLane) Steer(ctx context.Context, message string, images []ai.ImageContent) (string, error) {
	return lane.SteerText(ctx, message, images)
}
func (lane codingWorkerLane) FollowUp(ctx context.Context, message string, images []ai.ImageContent) (string, error) {
	return lane.FollowUpText(ctx, message, images)
}
func (lane codingWorkerLane) NextRun(ctx context.Context, message string, images []ai.ImageContent) (string, error) {
	return lane.NextRunText(ctx, message, images)
}
func (lane codingWorkerLane) CancelQueued(ctx context.Context, id string) (string, error) {
	result, err := lane.Lane.CancelQueued(ctx, id)
	return string(result), err
}
func (lane codingWorkerLane) Resume(ctx context.Context) (services.LaneOperation, error) {
	result, err := lane.Lane.Resume(ctx)
	if err != nil {
		return services.LaneOperation{}, err
	}
	return workerRunOperation(result), nil
}
func (lane codingWorkerLane) Compact(ctx context.Context, options *services.LaneCompactionOptions) (services.LaneOperation, error) {
	var instructions *string
	if options != nil {
		instructions = options.CustomInstructions
	}
	result, err := lane.Lane.Compact(ctx, instructions)
	if err != nil {
		return services.LaneOperation{}, err
	}
	return workerOperationRecord(result.Compaction), nil
}
func (lane codingWorkerLane) NavigateTree(ctx context.Context, target *string, options services.LaneNavigateOptions) (services.LaneOperation, error) {
	result, err := lane.Lane.NavigateTree(ctx, target, &agentharness.NavigateOptions{Summarize: options.Summarize, Label: options.Label, CustomInstructions: options.CustomInstructions})
	if err != nil {
		return services.LaneOperation{}, err
	}
	return workerOperationRecord(result.Navigation), nil
}
func (lane codingWorkerLane) SetModel(ctx context.Context, model services.ModelRef) error {
	return lane.Lane.SetModel(ctx, agentharness.ModelIdentity{Provider: model.Provider, ModelID: model.ModelId})
}
