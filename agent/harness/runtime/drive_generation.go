package runtime

// Ports packages/agent/src/harness/runtime/drive/generation.ts

import (
	"fmt"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/agentharness"
	"github.com/MichaelKinsy/PiG/agent/harness/execution"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
)

type preparedGeneration struct {
	model              *ai.Model
	tools              []ai.ToolSchema
	messages           []agent.AgentMessage
	systemPrompt       string
	streamOptions      harness.AgentHarnessStreamOptions
	toProviderMessages ProviderMessageConverter
}

func generationConfigurationError(code string, details any) session.OperationError {
	message := "The configured model is unavailable in this process"
	if code == "configured_tools_unavailable" {
		message = "One or more configured tools are unavailable in this process"
	}
	return session.OperationError{Code: code, Message: message, Details: &details}
}

func prepareGeneration(ctx harness.Context, lane *Lane, drive *Drive, generation session.OperationState) (*preparedGeneration, *session.OperationError, error) {
	identity := generation.GenerationContext.Configuration.Model
	model := lane.Models.GetModel(identity.Provider, identity.ModelID)
	if model == nil {
		return nil, new(generationConfigurationError("model_unavailable", identity)), nil
	}
	config := lane.ReadConfig()
	byName := map[string]harness.AgentHarnessTool{}
	for _, tool := range config.Tools {
		byName[tool.Name] = tool
	}
	missing := []string{}
	for _, name := range generation.GenerationContext.Configuration.ActiveToolNames {
		if _, ok := byName[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) != 0 {
		return nil, new(generationConfigurationError("configured_tools_unavailable", map[string]any{"tools": missing})), nil
	}
	tools := make([]ai.ToolSchema, 0, len(generation.GenerationContext.Configuration.ActiveToolNames))
	for _, name := range generation.GenerationContext.Configuration.ActiveToolNames {
		tool := byName[name]
		tools = append(tools, ai.ToolSchema{Name: tool.Name, Description: tool.Description, Parameters: tool.Parameters, ConstrainedSampling: tool.ConstrainedSampling})
	}
	messages, err := ReadBoundedContext(ctx, lane, drive, generation)
	if err != nil || messages.CancelRequested {
		return nil, nil, err
	}
	promptConfig := lane.ReadConfig()
	prompt := ""
	if promptConfig.SystemPrompt != nil {
		var toolContext any
		if promptConfig.ToolContext != nil {
			toolContext, err = promptConfig.ToolContext(ctx)
			if err != nil {
				return nil, nil, err
			}
		}
		prompt, err = promptConfig.SystemPrompt(ctx, toolContext)
		if err != nil {
			return nil, nil, err
		}
	}
	hook, err := lane.Hooks.RunBeforeRequest(ctx, drive.Gate, agentharness.BeforeRequestEvent{HookScope: agentharness.HookScope{Lane: lane.Name(), RunID: drive.OperationID}, Model: model, Step: "assistant", Attempt: generation.NextAttempt, StreamOptions: generation.GenerationContext.StreamOptions})
	if err != nil {
		return nil, nil, err
	}
	options := generation.GenerationContext.StreamOptions
	if hook != nil && hook.StreamOptions != nil {
		options = agentharness.ApplyStreamOptionsPatch(options, *hook.StreamOptions)
	}
	return &preparedGeneration{model: model, tools: tools, messages: messages.Value, systemPrompt: prompt, streamOptions: options, toProviderMessages: config.ToProviderMessages}, nil, nil
}

func publishGenerationIntent(ctx harness.Context, lane *Lane, drive *Drive, ready session.OperationState, prepared *preparedGeneration) (ContinueOperationResult[session.OperationState], error) {
	at := runtimeNow()
	responseID := lane.Session.IdGenerator().Next(&at)
	usageID := lane.Session.IdGenerator().Next(&at)
	return ContinueOperation(ctx, lane, ready, func(_ LaneState, current session.OperationState, _ session.OperationMeta, _ session.SessionReader) (OperationCommand[session.OperationState], error) {
		pending := session.OperationState{OperationScope: current.OperationScope, At: session.AtAssistantEffectPending, GenerationContext: ready.GenerationContext, Attempt: ready.NextAttempt, ResponseEntryID: responseID, UsageID: usageID, IntendedOutputLimit: prepared.model.Capabilities.MaxOutputTokens, ContextWindow: prepared.model.Capabilities.ContextWindow}
		return OperationCommand[session.OperationState]{Kind: CommandCommit, OperationState: pending, Materialize: func(session.CommitResult) session.OperationState { return pending }, Events: func(session.CommitResult) []agentharness.HarnessEvent {
			if ready.NextAttempt != 1 {
				return nil
			}
			return []agentharness.HarnessEvent{{Lane: lane.Name(), Payload: agentharness.TurnStartPayload{RunID: drive.OperationID, TurnID: ready.GenerationContext.StepID}}}
		}}, nil
	})
}

func performGeneration(ctx harness.Context, lane *Lane, drive *Drive, intent session.OperationState, prepared *preparedGeneration) (message *ai.AssistantMessage, err error) {
	response := OpenAssistantResponse(lane, drive, intent.ResponseEntryID, false)
	defer func() {
		if closeErr := response.Close(); closeErr != nil {
			err = closeErr
		}
	}()
	scope := agentharness.HookScope{Lane: lane.Name(), RunID: drive.OperationID}
	return execution.StreamHarnessAssistant(ctx, prepared.messages, execution.HarnessAssistantStreamConfig{
		Model: prepared.model, SystemPrompt: prepared.systemPrompt, Tools: prepared.tools, ThinkingLevel: intent.GenerationContext.Configuration.ThinkingLevel, StreamOptions: prepared.streamOptions,
		TransformContext: func(ctx harness.Context, request execution.AssistantRequestContext) (execution.AssistantRequestContext, error) {
			hook, err := lane.Hooks.RunTransformContext(ctx, drive.Gate, agentharness.TransformContextEvent{HookScope: scope, Messages: request.Messages, SystemPrompt: request.SystemPrompt})
			if err != nil {
				return execution.AssistantRequestContext{}, err
			}
			if hook != nil {
				if hook.Messages != nil {
					request.Messages = hook.Messages
				}
				if hook.SystemPrompt != nil {
					request.SystemPrompt = *hook.SystemPrompt
				}
			}
			return request, nil
		},
		ToProviderMessages: prepared.toProviderMessages,
		BeforePayload: func(ctx harness.Context, payload any, model *ai.Model) (any, error) {
			hook, err := lane.Hooks.RunBeforePayload(ctx, drive.Gate, agentharness.BeforePayloadEvent{HookScope: scope, Model: model, Payload: payload})
			if err != nil {
				return nil, err
			}
			if hook != nil {
				return hook.Payload, nil
			}
			return nil, nil
		},
		AfterResponse: response.AfterResponse,
		Request: func(ctx harness.Context, request ai.Context, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
			admitted := harness.WithAbortSignal(ctx, drive.Gate.Signal())
			options.SessionID = fmt.Sprintf("%s:%s", lane.Session.Metadata().ID, lane.Name())
			options.Signal = drive.Gate.Signal()
			options.TelemetryContext = harness.GetTelemetryContext(admitted)
			var stream *ai.AssistantMessageEventStream
			err := drive.Gate.Admit(func() error {
				stream = lane.Models.StreamSimple(admitted, prepared.model, request, options)
				return nil
			})
			return stream, err
		},
		Observer: response.Observer,
	})
}

// RunRetryWait either reports the durable deadline or admits a cancellable wait before publishing the next ready attempt.
func RunRetryWait(ctx harness.Context, lane *Lane, drive *Drive, generation session.OperationState) (ProcedureResult, error) {
	if runtimeNow() < generation.NotBefore {
		if !drive.WaitForRetry {
			return ProcedureResult{Kind: ProcedureWaiting, Outcome: agentharness.DriveOutcome{Kind: agentharness.DriveWaiting, OperationID: drive.OperationID, Reason: agentharness.DriveWaitRetry, NotBefore: generation.NotBefore}}, nil
		}
		if err := drive.Gate.Admit(func() error { return WaitUntil(drive.Gate.Signal(), generation.NotBefore) }); err != nil {
			return ProcedureResult{}, err
		}
	}
	result, err := ContinueOperation(ctx, lane, generation, func(_ LaneState, current session.OperationState, _ session.OperationMeta, _ session.SessionReader) (OperationCommand[ProcedureResult], error) {
		next := session.OperationState{OperationScope: current.OperationScope, At: session.AtAssistantReady, GenerationContext: generation.GenerationContext, NextAttempt: generation.NextAttempt}
		return OperationCommand[ProcedureResult]{Kind: CommandCommit, OperationState: next, Materialize: func(session.CommitResult) ProcedureResult { return ProcedureResult{Kind: ProcedureContinue} }, Events: func(session.CommitResult) []agentharness.HarnessEvent {
			return []agentharness.HarnessEvent{{Lane: lane.Name(), Payload: agentharness.RetryStartPayload{RunID: drive.OperationID, Step: generation.GenerationContext.StepID, Attempt: generation.NextAttempt}}}
		}}, nil
	})
	if result.CancelRequested {
		return ProcedureResult{Kind: ProcedureContinue}, err
	}
	return result.Value, err
}

// RunGeneration executes one ready assistant request or advances its durable retry wait.
func RunGeneration(ctx harness.Context, lane *Lane, drive *Drive, generation session.OperationState) (ProcedureResult, error) {
	if generation.At == session.AtAssistantRetryWait {
		return RunRetryWait(ctx, lane, drive, generation)
	}
	prepared, failure, err := prepareGeneration(ctx, lane, drive, generation)
	if err != nil {
		return ProcedureResult{}, err
	}
	if failure != nil {
		return PublishConfigurationFailure(ctx, lane, drive, generation, *failure)
	}
	if prepared == nil {
		return ProcedureResult{Kind: ProcedureContinue}, nil
	}
	intent, err := publishGenerationIntent(ctx, lane, drive, generation, prepared)
	if err != nil {
		return ProcedureResult{}, err
	}
	if intent.CancelRequested {
		return ProcedureResult{Kind: ProcedureContinue}, nil
	}
	response, err := performGeneration(ctx, lane, drive, intent.Value, prepared)
	if err != nil {
		return ProcedureResult{}, err
	}
	return PublishResponse(ctx, lane, drive, intent.Value, response, false)
}
