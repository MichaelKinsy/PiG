// Ports packages/durable/src/harness/generation.ts.

package harness

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"

	"github.com/MichaelKinsy/PiG/ai"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
)

// GenerationInput is the input of the built-in generation task, Pi's Record<string, never> (generation.ts:49): an object
// with no members, created as {} (generation.ts:675). The run's inputs live in pi.live.run.
type GenerationInput map[string]struct{}

// GenerationPhase names a phase of the generation task (generation.ts:49-90).
type GenerationPhase string

const (
	generationPrepare GenerationPhase = "prepare"
	generationRequest GenerationPhase = "request"
	generationRetry   GenerationPhase = "retry"
	generationPoll    GenerationPhase = "poll"
	generationTools   GenerationPhase = "tools"
)

// GenerationCheckpoint is the durable checkpoint of the generation task (generation.ts:49-90). Phase selects the
// members in use:
//   - prepare: Attempt; Compacted is the blocking compaction this generation waited for, which starts no other
//     compaction (spec §8.3); Overflow is the error text of the overflow that started Compacted, checked once when
//     prepare resumes; Threshold is set when the blocking threshold started Compacted, whose outcome is checked once
//     when prepare resumes.
//   - request: Attempt, Compacted, Model, ThinkingLevel, StreamOptions (the settings' request options when
//     preparation committed; a resend after recovery uses them unchanged), and Cutoff, the newest entry included in
//     the request.
//   - retry: Attempt, Compacted, Until.
//   - poll: Attempt, Compacted, Model, Cutoff, Handle, PollAt.
//   - tools: Assistant (the tool-calling answer), Tools (tool tasks created so far, in call order) and Pending (calls of
//     a sequential round not started yet, in call order). The generation owns the tool tasks (spec §8.5).
type GenerationCheckpoint struct {
	Phase         GenerationPhase                    `json:"phase"`
	Attempt       int                                `json:"attempt,omitempty"`
	Compacted     *durable.TaskId                    `json:"compacted,omitempty"`
	Overflow      *string                            `json:"overflow,omitempty"`
	Threshold     bool                               `json:"threshold,omitempty"`
	Model         *durable.ModelRef                  `json:"model,omitempty"`
	ThinkingLevel ai.ModelThinkingLevel              `json:"thinkingLevel,omitempty"`
	StreamOptions *durable.ConversationStreamOptions `json:"streamOptions,omitempty"`
	Cutoff        *durable.EntryId                   `json:"cutoff,omitempty"`
	Until         *float64                           `json:"until,omitempty"`
	Handle        *ai.DeferredHandle                 `json:"handle,omitempty"`
	PollAt        *float64                           `json:"pollAt,omitempty"`
	Assistant     *durable.EntryId                   `json:"assistant,omitempty"`
	Tools         []durable.TaskId                   `json:"tools,omitempty"`
	Pending       []string                           `json:"pending,omitempty"`
}

// MarshalJSON keeps the tools phase's tools and pending arrays when they are empty.
func (checkpoint GenerationCheckpoint) MarshalJSON() ([]byte, error) {
	type plain GenerationCheckpoint
	if checkpoint.Phase != generationTools {
		return marshalPlain(plain(checkpoint))
	}
	return marshalPlain(struct {
		Phase     GenerationPhase  `json:"phase"`
		Assistant *durable.EntryId `json:"assistant,omitempty"`
		Tools     []durable.TaskId `json:"tools"`
		Pending   []string         `json:"pending"`
	}{checkpoint.Phase, checkpoint.Assistant, nonNil(checkpoint.Tools), nonNil(checkpoint.Pending)})
}

// GenerationResult is the result of the generation task: the answer, or the tool-calling answer of its round.
type GenerationResult struct {
	EntryId durable.EntryId `json:"entryId"`
}

type generationRuntime = durable.TaskRuntime[GenerationInput, GenerationCheckpoint, GenerationResult, *GenerationHooks]
type generationNext = durable.NextTaskState[GenerationCheckpoint, GenerationResult]

// generationRequestInfo is what classification needs from the request that produced a message.
type generationRequestInfo struct {
	attempt   int
	compacted *durable.TaskId
	model     durable.ModelRef
	cutoff    durable.EntryId
	// messages is the committed model context through cutoff, when the phase already derived it.
	messages []ai.Message
	// pollAt is set when the message came from polling, so a still deferred result polls strictly later.
	pollAt *float64
}

const (
	defaultPollAfter  = 5000
	generationKind    = "pi.generation"
	noModelReason     = "no_model"
	modelErrorReason  = "model_error"
	toolUnavailable   = "tool_unavailable"
	abortedResultCode = "aborted"
)

// GenerationTask is the built-in generation task (generation.ts:107-273): it prepares the positional system prompt
// and tool loadout, requests or polls the model, retries, and classifies the response. The run's inputs live in
// pi.live.run. Its handlers create successor generations, so the definition is assigned in init.
var GenerationTask durable.Task[GenerationInput, GenerationCheckpoint, GenerationResult, *GenerationHooks]

func init() {
	GenerationTask = durable.DefineTask(durable.TaskDefinition[GenerationInput, GenerationCheckpoint, GenerationResult, *GenerationHooks]{
		Name:    generationKind,
		Version: 1,
		Initial: func(GenerationInput) GenerationCheckpoint {
			return GenerationCheckpoint{Phase: generationPrepare, Attempt: 1}
		},
		Phases: map[string]durable.PhaseHandler[GenerationInput, GenerationCheckpoint, GenerationResult, *GenerationHooks]{
			string(generationPrepare): generationPrepareHandler,
			string(generationRequest): generationRequestHandler,
			string(generationRetry):   generationRetryHandler,
			string(generationPoll):    generationPollHandler,
			string(generationTools):   generationToolsHandler,
		},
		Abort: generationAbortHandler,
	})
}

func checkpointOf(task durable.RunningTask[GenerationInput, GenerationCheckpoint, GenerationResult]) GenerationCheckpoint {
	if task.State.Checkpoint == nil {
		return GenerationCheckpoint{}
	}
	return *task.State.Checkpoint
}

func sleepingState[S, R any](checkpoint S, until float64) *durable.NextTaskState[S, R] {
	return &durable.NextTaskState[S, R]{Status: durable.TaskSleeping, Checkpoint: &checkpoint, Until: &until}
}

func runningState[S, R any](checkpoint S) *durable.NextTaskState[S, R] {
	return &durable.NextTaskState[S, R]{Status: durable.TaskRunning, Checkpoint: &checkpoint}
}

func waitingState[S, R any](checkpoint S, on []durable.TaskId, policy durable.JoinPolicy) *durable.NextTaskState[S, R] {
	return &durable.NextTaskState[S, R]{Status: durable.TaskWaiting, Checkpoint: &checkpoint, On: nonNil(on), Policy: policy}
}

func completedState[S, R any](result R) *durable.NextTaskState[S, R] {
	return &durable.NextTaskState[S, R]{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[R]{Status: durable.OutcomeCompleted, Result: &result}}
}

func failedState[S, R any](message, reason string) *durable.NextTaskState[S, R] {
	return &durable.NextTaskState[S, R]{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[R]{
		Status: durable.OutcomeFailed,
		Error:  &durable.TaskOutcomeError{Message: message, Detail: map[string]any{"reason": reason}},
	}}
}

func abortedState[S, R any]() *durable.NextTaskState[S, R] {
	return &durable.NextTaskState[S, R]{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[R]{Status: durable.OutcomeAborted}}
}

// generationPrepareHandler renders the system prompt and tool loadout and appends the positional pi.system entries
// they need, then moves to request (generation.ts:113-172). The agent and settings resolved here are fixed for this
// request. Only the Harness writes to a busy conversation, so the transcript read here is still the tail at the commit.
func generationPrepareHandler(ctx context.Context, task durable.RunningTask[GenerationInput, GenerationCheckpoint, GenerationResult], runtime generationRuntime) error {
	conversationId := runtime.ConversationId()
	agent, err := runtime.Agent(ctx)
	if err != nil {
		return err
	}
	settings := runtime.Settings()
	ref := agent.Model
	var resolved *ai.Model
	if ref != nil {
		resolved = runtime.Models().GetModel(ref.Provider, ref.ModelId)
	}
	if ref == nil || resolved == nil {
		return failGenerationNoModel(ctx, runtime, ref)
	}
	checkpoint := checkpointOf(task)
	attempt, compacted := checkpoint.Attempt, checkpoint.Compacted
	if compacted != nil && checkpoint.Overflow != nil {
		outcomes, err := runtime.Outcomes(ctx, []durable.TaskId{*compacted})
		if err != nil {
			return err
		}
		result, _ := decodeOutcomeResult[CompactionResult](outcomes[0])
		if outcomes[0].Status != durable.OutcomeCompleted || result == nil || result.EntryId == nil {
			return failGenerationModelError(ctx, runtime, *checkpoint.Overflow)
		}
	}
	// A threshold compaction that found nothing to compact or was declined leaves a request that may still fit. One
	// that failed left a context it judged too large: sending it would at best leave no room for the answer.
	if compacted != nil && checkpoint.Threshold {
		outcomes, err := runtime.Outcomes(ctx, []durable.TaskId{*compacted})
		if err != nil {
			return err
		}
		if outcomes[0].Status != durable.OutcomeCompleted {
			return failGenerationModelError(ctx, runtime, compactionFailure(outcomes[0]))
		}
	}
	view, err := runtime.Context(sharedReads(ctx), conversationId, nil)
	if err != nil {
		return err
	}
	shown := ReplaySections(view.Messages)
	environment, err := runtime.Env(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return err
		}
		runtime.Report(err, durable.ReportFrom{Type: durable.ReportFromHost, Callback: durable.HostCallbackEnv})
		environment = nil
	}
	shownMap := map[string]string{}
	for _, key := range shown.Keys() {
		shownMap[key], _ = shown.Get(key)
	}
	input := durable.PromptInput{ConversationId: conversationId, Agent: agent, Env: environment, Shown: shownMap, Read: runtime}
	desired, err := RenderSections(ctx, agent.Sections, input, shown, func(err error, section *durable.PromptSection) {
		runtime.Report(err, durable.ReportFrom{Type: durable.ReportFromExtension, Extension: SectionExtension(agent, section.Key)})
	})
	if err != nil {
		return err
	}
	offered := make([]ai.ToolSchema, 0, len(agent.Tools))
	for _, tool := range agent.Tools {
		offered = append(offered, tool.ToolSchema)
	}
	entries := PlanSystemEntries(view, desired, offered, int64(runtime.Now()))
	threshold := ""
	if compacted == nil {
		var planned []ai.Message
		for _, entry := range entries {
			planned = append(planned, entry.Model...)
		}
		threshold = thresholdCompaction(view, planned, resolved.Capabilities.ContextWindow, settings.Compaction.PolicyFor(*ref))
	}
	if threshold == "blocking" {
		// Compact first and prepare again; the transcript is unchanged until the compaction appends.
		return runtime.Commit(ctx, func(tx durable.Tx, _ durable.RunningTask[GenerationInput, GenerationCheckpoint, GenerationResult]) (*generationNext, error) {
			owner := runtime.TaskId()
			child, err := CreateCompaction(tx, conversationId, CompactionInput{Reason: durable.CompactionThreshold}, &owner)
			if err != nil {
				return nil, err
			}
			next := GenerationCheckpoint{Phase: generationPrepare, Attempt: attempt, Compacted: &child, Threshold: true}
			return waitingState[GenerationCheckpoint, GenerationResult](next, []durable.TaskId{child}, durable.JoinAllSettled), nil
		})
	}
	return runtime.Commit(ctx, func(tx durable.Tx, _ durable.RunningTask[GenerationInput, GenerationCheckpoint, GenerationResult]) (*generationNext, error) {
		page, err := tx.ScanEntries(durable.EntryQuery{ConversationId: conversationId}, 1, nil)
		if err != nil {
			return nil, err
		}
		var cutoff *durable.EntryId
		if len(page.Items) > 0 {
			id := page.Items[0].Id
			cutoff = &id
		}
		for _, entry := range entries {
			appended, err := durable.TxAppendEntry(tx, durable.SystemEntry, conversationId, entry)
			if err != nil {
				return nil, err
			}
			id := appended.Id
			cutoff = &id
		}
		if cutoff == nil {
			return nil, fmt.Errorf("Conversation %d has no entries to send", conversationId)
		}
		// Checked in this commit, so a compaction admitted during preparation counts.
		if threshold == "background" {
			live, err := docDraft(tx, LiveDoc, conversationId)
			if err != nil {
				return nil, err
			}
			if !live.Has("compactions") {
				if _, err := CreateCompaction(tx, conversationId, CompactionInput{Reason: durable.CompactionThreshold}, nil); err != nil {
					return nil, err
				}
			}
		}
		streamOptions := settings.Stream
		model := *ref
		next := GenerationCheckpoint{
			Phase:         generationRequest,
			Attempt:       attempt,
			Compacted:     compacted,
			Model:         &model,
			ThinkingLevel: agent.ThinkingLevel,
			StreamOptions: &streamOptions,
			Cutoff:        cutoff,
		}
		return runningState[GenerationCheckpoint, GenerationResult](next), nil
	})
}

func generationRequestHandler(ctx context.Context, task durable.RunningTask[GenerationInput, GenerationCheckpoint, GenerationResult], runtime generationRuntime) error {
	checkpoint := checkpointOf(task)
	conversationId := runtime.ConversationId()
	if err := runtime.Commit(ctx, func(tx durable.Tx, _ durable.RunningTask[GenerationInput, GenerationCheckpoint, GenerationResult]) (*generationNext, error) {
		live, err := docDraft(tx, LiveDoc, conversationId)
		if err != nil {
			return nil, err
		}
		if err := ConvertPartial(tx, live, conversationId); err != nil {
			return nil, err
		}
		return nil, live.Set("generation", delta.JsonObjectOf("attempt", float64(checkpoint.Attempt)))
	}); err != nil {
		return err
	}
	ref := *checkpoint.Model
	model := runtime.Models().GetModel(ref.Provider, ref.ModelId)
	if model == nil {
		return failGenerationNoModel(ctx, runtime, &ref)
	}
	view, err := runtime.Context(sharedReads(ctx), conversationId, &durable.ContextOptions{At: checkpoint.Cutoff})
	if err != nil {
		return err
	}
	// view.Messages is shared with the kept context range. A hook may modify what it receives, so the first one gets a copy;
	// until then the request is frozen and the provider path shares the messages.
	messages, shared := view.Messages, true
	if err := runtime.Hooks().Each("beforeRequest", func(hooks *GenerationHooks) error {
		if hooks == nil || hooks.BeforeRequest == nil {
			return nil
		}
		if shared {
			messages, shared = ai.NormalizeContext(ai.Context{Messages: messages}).Messages(), false
		}
		replaced, err := hooks.BeforeRequest(ctx, GenerationRequest{Messages: messages}, runtime)
		if err != nil {
			return err
		}
		if replaced != nil {
			messages = replaced.Messages
		}
		return nil
	}); err != nil {
		return err
	}
	var streamOptions durable.ConversationStreamOptions
	if checkpoint.StreamOptions != nil {
		streamOptions = *checkpoint.StreamOptions
	}
	options := streamOptionsOf(streamOptions, runtime.Signal(), checkpoint.ThinkingLevel)
	options.Telemetry = chordctx.Telemetry(ctx)
	if options.SessionID, err = ensureProviderSessionId(ctx, runtime); err != nil {
		return err
	}
	message, err := streamResponse(ctx, runtime, model, messages, shared, options, checkpoint.Attempt)
	if err != nil {
		return err
	}
	request := generationRequestInfo{attempt: checkpoint.Attempt, compacted: checkpoint.Compacted, model: ref, cutoff: *checkpoint.Cutoff, messages: view.Messages}
	return classify(ctx, runtime, request, message)
}

func generationRetryHandler(ctx context.Context, task durable.RunningTask[GenerationInput, GenerationCheckpoint, GenerationResult], runtime generationRuntime) error {
	checkpoint := checkpointOf(task)
	// After the backoff's sleep; a run before Until (an early wake, or a record of an earlier version) sleeps again.
	if *checkpoint.Until > runtime.Now() {
		return runtime.SleepUntil(ctx, *checkpoint.Until, checkpoint)
	}
	return runtime.Commit(ctx, func(tx durable.Tx, _ durable.RunningTask[GenerationInput, GenerationCheckpoint, GenerationResult]) (*generationNext, error) {
		live, err := docDraft(tx, LiveDoc, runtime.ConversationId())
		if err != nil {
			return nil, err
		}
		if err := live.Set("generation", delta.JsonObjectOf("attempt", float64(checkpoint.Attempt+1))); err != nil {
			return nil, err
		}
		next := GenerationCheckpoint{Phase: generationPrepare, Attempt: checkpoint.Attempt + 1, Compacted: checkpoint.Compacted}
		return runningState[GenerationCheckpoint, GenerationResult](next), nil
	})
}

func generationPollHandler(ctx context.Context, task durable.RunningTask[GenerationInput, GenerationCheckpoint, GenerationResult], runtime generationRuntime) error {
	checkpoint := checkpointOf(task)
	ref := *checkpoint.Model
	// After the sleep until PollAt; a run before it (an early wake, or a record of an earlier version) sleeps again.
	if *checkpoint.PollAt > runtime.Now() {
		return runtime.SleepUntil(ctx, *checkpoint.PollAt, checkpoint)
	}
	model := runtime.Models().GetModel(ref.Provider, ref.ModelId)
	if model == nil {
		return failGenerationNoModel(ctx, runtime, &ref)
	}
	signal := runtime.Signal()
	message := runtime.Models().FetchDeferred(signal, model, *checkpoint.Handle, ai.DeferredFetchOptions{StreamOptions: ai.StreamOptions{Signal: signal, Telemetry: chordctx.Telemetry(ctx)}})
	if message == nil {
		return errors.New("deferred fetch returned no message")
	}
	request := generationRequestInfo{attempt: checkpoint.Attempt, compacted: checkpoint.Compacted, model: ref, cutoff: *checkpoint.Cutoff, pollAt: checkpoint.PollAt}
	return classify(ctx, runtime, request, *message)
}

// generationToolsHandler waits on the round's tool tasks; a sequential round starts its next call and waits for it.
func generationToolsHandler(ctx context.Context, task durable.RunningTask[GenerationInput, GenerationCheckpoint, GenerationResult], runtime generationRuntime) error {
	checkpoint := checkpointOf(task)
	assistant := *checkpoint.Assistant
	if len(checkpoint.Pending) == 0 {
		return finishToolRound(ctx, runtime, assistant, checkpoint.Tools)
	}
	next, rest := checkpoint.Pending[0], checkpoint.Pending[1:]
	return runtime.Commit(ctx, func(tx durable.Tx, _ durable.RunningTask[GenerationInput, GenerationCheckpoint, GenerationResult]) (*generationNext, error) {
		live, err := docDraft(tx, LiveDoc, runtime.ConversationId())
		if err != nil {
			return nil, err
		}
		taskId, err := createToolTask(tx, runtime.TaskId(), assistant, next)
		if err != nil {
			return nil, err
		}
		if slots := live.Array("tools"); slots != nil {
			for index := range slots.Len() {
				slot := slots.Object(index)
				if slot != nil && slot.Get("callId") == next && !slot.Has("taskId") {
					if err := slot.Set("taskId", float64(taskId)); err != nil {
						return nil, err
					}
					break
				}
			}
		}
		tools := append(append([]durable.TaskId{}, checkpoint.Tools...), taskId)
		state := GenerationCheckpoint{Phase: generationTools, Assistant: &assistant, Tools: tools, Pending: append([]string{}, rest...)}
		return waitingState[GenerationCheckpoint, GenerationResult](state, []durable.TaskId{taskId}, durable.JoinAllSettled), nil
	})
}

// generationAbortHandler runs after the round's tool tasks are terminal; calls never started get aborted results
// (spec §8.5, generation.ts:236-272).
func generationAbortHandler(ctx context.Context, task durable.RunningTask[GenerationInput, GenerationCheckpoint, GenerationResult], runtime generationRuntime) error {
	checkpoint := checkpointOf(task)
	if checkpoint.Phase == generationPoll && checkpoint.Model != nil && checkpoint.Handle != nil {
		if model := runtime.Models().GetModel(checkpoint.Model.Provider, checkpoint.Model.ModelId); model != nil {
			signal := runtime.Signal()
			if err := runtime.Models().CancelDeferred(signal, model, *checkpoint.Handle, ai.DeferredCancelOptions{Signal: signal, Telemetry: chordctx.Telemetry(ctx)}); err != nil {
				runtime.Report(err, durable.ReportFrom{Type: durable.ReportFromHost, Callback: durable.HostCallbackModels})
			}
		}
	}
	conversationId := runtime.ConversationId()
	var unstarted []ai.ToolCall
	if checkpoint.Phase == generationTools && checkpoint.Assistant != nil {
		calls, err := readCalls(ctx, runtime, *checkpoint.Assistant, checkpoint.Pending)
		if err != nil {
			return err
		}
		unstarted = calls
	}
	return runtime.Commit(ctx, func(tx durable.Tx, _ durable.RunningTask[GenerationInput, GenerationCheckpoint, GenerationResult]) (*generationNext, error) {
		live, err := docDraft(tx, LiveDoc, conversationId)
		if err != nil {
			return nil, err
		}
		if err := ConvertPartial(tx, live, conversationId); err != nil {
			return nil, err
		}
		for _, call := range unstarted {
			result := HarnessError(abortedResultCode, fmt.Sprintf("Tool %s was aborted", call.Name))
			if _, err := AppendToolResult(tx, conversationId, call, result, runtime.Now(), nil); err != nil {
				return nil, err
			}
		}
		if err := EndRun(tx, live, runtime.TaskId(), durable.SubmissionSettlement{Status: durable.SubmissionUnanswered, Reason: "aborted"}); err != nil {
			return nil, err
		}
		return abortedState[GenerationCheckpoint, GenerationResult](), nil
	})
}

// readCalls returns the calls callIds of the assistant entry, in the given order.
func readCalls(ctx context.Context, runtime generationRuntime, assistant durable.EntryId, callIds []string) ([]ai.ToolCall, error) {
	entry, err := durable.TaskEntry(ctx, runtime, durable.AssistantEntry, assistant)
	if err != nil {
		return nil, err
	}
	var calls []ai.ToolCall
	if entry != nil && len(entry.Model) > 0 {
		if message, ok := entry.Model[0].(ai.AssistantMessage); ok {
			calls = toolCallsOf(message)
		}
	}
	var ordered []ai.ToolCall
	for _, id := range callIds {
		for _, call := range calls {
			if call.ID == id {
				ordered = append(ordered, call)
				break
			}
		}
	}
	return ordered, nil
}

func toolCallsOf(message ai.AssistantMessage) []ai.ToolCall {
	var calls []ai.ToolCall
	for _, content := range message.Content {
		switch call := content.(type) {
		case ai.ToolCall:
			calls = append(calls, call)
		case *ai.ToolCall:
			calls = append(calls, *call)
		}
	}
	return calls
}

// createToolTask creates a tool task for call callId, owned by the generation.
func createToolTask(tx durable.Tx, owner durable.TaskId, assistant durable.EntryId, callId string) (durable.TaskId, error) {
	input, err := durable.ToJsonValue(ToolTaskInput{Kind: ToolTaskModel, Assistant: assistant, CallId: callId})
	if err != nil {
		return 0, err
	}
	return tx.CreateTaskErased(ToolTask.Erased(), input, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByTask, TaskId: owner}})
}

// thresholdCompaction returns which threshold compaction preparation starts before its request (spec §8.3,
// generation.ts:299-318): blocking above contextWindow - reserveTokens, background above the background threshold, and
// only when range selection finds a cut. The caller starts a background one only while no compaction is listed.
func thresholdCompaction(view durable.ContextView, planned []ai.Message, contextWindow int, policy durable.CompactionPolicy) string {
	if !policy.Enabled || contextWindow <= 0 {
		return ""
	}
	tokens := EstimateContext(view, planned)
	blocking := contextWindow - policy.ReserveTokens
	background := blocking - policy.BackgroundTokens
	over := ""
	if tokens > blocking {
		over = "blocking"
	} else if policy.BackgroundTokens > 0 && tokens > background {
		over = "background"
	}
	if over == "" {
		return ""
	}
	if _, ok := SelectCut(view, policy.KeepRecentTokens); !ok {
		return ""
	}
	return over
}

// failGenerationModelError settles the run's inputs unanswered with model_error and fails with text.
func failGenerationModelError(ctx context.Context, runtime generationRuntime, text string) error {
	return runtime.Commit(ctx, func(tx durable.Tx, _ durable.RunningTask[GenerationInput, GenerationCheckpoint, GenerationResult]) (*generationNext, error) {
		failure, err := prepareFailure(tx, runtime)
		if err != nil {
			return nil, err
		}
		live, err := docDraft(tx, LiveDoc, runtime.ConversationId())
		if err != nil {
			return nil, err
		}
		if err := endFailedRun(tx, runtime, live, failure, durable.SubmissionSettlement{Status: durable.SubmissionUnanswered, Reason: modelErrorReason, Detail: text}); err != nil {
			return nil, err
		}
		return failedState[GenerationCheckpoint, GenerationResult](text, modelErrorReason), nil
	})
}

// failure is what a failed run's end needs from the table, read before the commit's first table write.
type failure struct {
	policy   durable.AfterFailedRun
	boundary *Boundary
}

// prepareFailure reads the boundary a failed run's end may apply, only under the continue policy (generation.ts
// prepareFailure).
func prepareFailure(tx durable.Tx, runtime generationRuntime) (failure, error) {
	policy := runtime.Settings().AfterFailedRun
	if policy != durable.AfterFailedRunContinue {
		return failure{policy: policy}, nil
	}
	boundary, err := PrepareBoundary(tx, runtime.ConversationId(), QueueModesOf(runtime.Settings()))
	return failure{policy: policy, boundary: boundary}, err
}

// endFailedRun ends a failed run, settling its inputs with settlement, and does with the inputs queued behind it what
// settings.afterFailedRun says (spec section 6): keep them, start the next run with them, or withdraw them (generation.ts
// endFailedRun).
func endFailedRun(tx durable.Tx, runtime generationRuntime, live *delta.Object, failed failure, settlement durable.SubmissionSettlement) error {
	conversationId := runtime.ConversationId()
	if err := EndRun(tx, live, runtime.TaskId(), settlement); err != nil {
		return err
	}
	if failed.policy == durable.AfterFailedRunWithdraw {
		if err := WithdrawQueuedInputsFor(tx, conversationId, "run_failed"); err != nil {
			return err
		}
	}
	if failed.boundary == nil {
		return nil
	}
	placed, err := ApplyBoundary(tx, failed.boundary, BoundaryFinal, runtime.Now())
	if err != nil {
		return err
	}
	if len(placed.Users) > 0 {
		return StartRun(tx, conversationId, live, placed.Users)
	}
	return nil
}

// noModelMessage is the failure text when a model is unset or unknown.
func noModelMessage(ref *durable.ModelRef) string {
	if ref == nil {
		return "No model is configured"
	}
	return fmt.Sprintf("Model %s/%s is not available", ref.Provider, ref.ModelId)
}

// failGenerationNoModel settles the run's inputs unanswered with no_model and fails.
func failGenerationNoModel(ctx context.Context, runtime generationRuntime, ref *durable.ModelRef) error {
	message := noModelMessage(ref)
	return runtime.Commit(ctx, func(tx durable.Tx, _ durable.RunningTask[GenerationInput, GenerationCheckpoint, GenerationResult]) (*generationNext, error) {
		failed, err := prepareFailure(tx, runtime)
		if err != nil {
			return nil, err
		}
		live, err := docDraft(tx, LiveDoc, runtime.ConversationId())
		if err != nil {
			return nil, err
		}
		if err := endFailedRun(tx, runtime, live, failed, durable.SubmissionSettlement{Status: durable.SubmissionUnanswered, Reason: noModelReason}); err != nil {
			return nil, err
		}
		return failedState[GenerationCheckpoint, GenerationResult](message, noModelReason), nil
	})
}

// ConvertPartial appends a committed partial left by an interrupted, aborted, faulted, or orphaned attempt as an
// aborted assistant entry; the caller replaces or removes generation (generation.ts:346-352).
func ConvertPartial(tx durable.Tx, live *delta.Object, conversationId durable.ConversationId) error {
	generation := live.Object("generation")
	if generation == nil {
		return nil
	}
	partial := generation.Object("message")
	if partial == nil {
		return nil
	}
	message, err := durable.FromJsonValue[ai.AssistantMessage](partial.Snapshot())
	if err != nil {
		return err
	}
	message.StopReason = ai.StopReasonAborted
	_, err = appendAssistant(tx, conversationId, message)
	return err
}

// streamResponse streams one request and returns the terminal message (generation.ts:354-405). Partials commit as
// trailing writes at most every Settings.Progress.PartialIntervalMs (default 100 ms) with one commit in flight; stopping the throttle waits for that commit, so no
// stale partial lands after the outcome.
func streamResponse(ctx context.Context, runtime generationRuntime, model *ai.Model, messages []ai.Message, frozen bool, options ai.StreamOptions, attempt int) (ai.AssistantMessage, error) {
	throttle := &partialThrottle{runtime: runtime, ctx: ctx, attempt: attempt, interval: runtime.Settings().Progress.PartialIntervalMs}
	defer throttle.stop()
	signal := runtime.Signal()
	if !frozen {
		messages = append([]ai.Message{}, messages...)
	}
	stream := runtime.Models().StreamSimple(signal, model, ai.Context{Messages: messages, Frozen: frozen}, options)
	for event := range stream.Events(signal) {
		switch event.(type) {
		case ai.DoneEvent, *ai.DoneEvent, ai.ErrorEvent, *ai.ErrorEvent:
			continue
		}
		// A partial without content, such as pi-ai's opening start event, shows nothing; a deferred response never
		// gets past it, so it never leaves a partial.
		partial := partialOf(event)
		if partial == nil || len(partial.Content) == 0 {
			continue
		}
		throttle.offer(partial)
	}
	result, err := stream.ResultContext(signal)
	if err != nil {
		return ai.AssistantMessage{}, err
	}
	if result == nil {
		return ai.AssistantMessage{}, errors.New("stream ended without a result")
	}
	return *result, nil
}

func partialOf(event ai.AssistantMessageEvent) *ai.AssistantMessage {
	switch value := event.(type) {
	case ai.StartEvent:
		return value.Partial
	case ai.TextStartEvent:
		return value.Partial
	case ai.TextDeltaEvent:
		return value.Partial
	case ai.TextEndEvent:
		return value.Partial
	case ai.ThinkingStartEvent:
		return value.Partial
	case ai.ThinkingDeltaEvent:
		return value.Partial
	case ai.ThinkingEndEvent:
		return value.Partial
	case ai.ToolCallStartEvent:
		return value.Partial
	case ai.ToolCallDeltaEvent:
		return value.Partial
	case ai.ToolCallEndEvent:
		return value.Partial
	}
	return nil
}

// partialThrottle commits the newest partial at most every interval with one commit in flight.
type partialThrottle struct {
	runtime  generationRuntime
	ctx      context.Context
	attempt  int
	interval float64
	mu       sync.Mutex
	pending  *ai.AssistantMessage
	disarm   func()
	inFlight chan struct{}
	stopped  bool
}

// offer records the newest partial. Holding it until the flush is race-free: a partial from a producer outside the
// executor is an immutable emission-time snapshot, and a live view encodes the stream's current publication under
// its cell lock, as generation.ts:378 copies the provider's mutable partial at flush time. The flush copies it once.
func (throttle *partialThrottle) offer(partial *ai.AssistantMessage) {
	throttle.mu.Lock()
	defer throttle.mu.Unlock()
	throttle.pending = partial
	if throttle.disarm == nil && throttle.inFlight == nil && !throttle.stopped {
		throttle.disarm = throttle.runtime.SetTimer(throttle.flush, throttle.interval)
	}
}

func (throttle *partialThrottle) flush() {
	throttle.mu.Lock()
	throttle.disarm = nil
	partial := throttle.pending
	throttle.pending = nil
	if partial == nil || throttle.stopped {
		throttle.mu.Unlock()
		return
	}
	done := make(chan struct{})
	throttle.inFlight = done
	throttle.mu.Unlock()
	go func() {
		defer close(done)
		runtime := throttle.runtime
		message, err := durable.ToJsonObject(partial)
		if err == nil {
			err = runtime.Commit(throttle.ctx, func(tx durable.Tx, _ durable.RunningTask[GenerationInput, GenerationCheckpoint, GenerationResult]) (*generationNext, error) {
				live, err := docDraft(tx, LiveDoc, runtime.ConversationId())
				if err != nil {
					return nil, err
				}
				generation := live.Object("generation")
				if generation == nil {
					if err := live.Set("generation", delta.JsonObjectOf("attempt", float64(throttle.attempt))); err != nil {
						return nil, err
					}
					generation = live.Object("generation")
				}
				return nil, AssignJson(generation, "message", message)
			})
		}
		// Rejections after an abort mark or close are expected; the committed state stays consistent.
		if err != nil && runtime.Signal().Err() == nil {
			runtime.Report(err)
		}
		throttle.mu.Lock()
		throttle.inFlight = nil
		if throttle.pending != nil && !throttle.stopped {
			throttle.disarm = throttle.runtime.SetTimer(throttle.flush, throttle.interval)
		}
		throttle.mu.Unlock()
	}()
}

// stop stops the throttle and waits for the commit in flight.
func (throttle *partialThrottle) stop() {
	throttle.mu.Lock()
	throttle.stopped = true
	if throttle.disarm != nil {
		throttle.disarm()
		throttle.disarm = nil
	}
	inFlight := throttle.inFlight
	throttle.mu.Unlock()
	if inFlight != nil {
		<-inFlight
	}
}

// classify classifies a terminal provider message in one commit that also clears the partial (generation.ts:408-488).
func classify(ctx context.Context, runtime generationRuntime, request generationRequestInfo, message ai.AssistantMessage) error {
	// An abort mark or close: the abort invocation or the reopened run handles the committed state.
	if err := runtime.Signal().Err(); err != nil {
		return context.Cause(runtime.Signal())
	}
	conversationId := runtime.ConversationId()
	attempt, compacted, ref, cutoff := request.attempt, request.compacted, request.model, request.cutoff
	if message.StopReason == ai.StopReasonDeferred && message.Deferred != nil {
		handle := *message.Deferred
		pollAfter := float64(defaultPollAfter)
		if handle.PollAfterMS != nil {
			pollAfter = float64(*handle.PollAfterMS)
		}
		earliest := math.Inf(-1)
		if request.pollAt != nil {
			earliest = *request.pollAt + 1
		}
		pollAt := math.Max(runtime.Now()+pollAfter, earliest)
		return runtime.Commit(ctx, func(tx durable.Tx, _ durable.RunningTask[GenerationInput, GenerationCheckpoint, GenerationResult]) (*generationNext, error) {
			live, err := docDraft(tx, LiveDoc, conversationId)
			if err != nil {
				return nil, err
			}
			if err := live.Set("generation", delta.JsonObjectOf("attempt", float64(attempt), "deferred", delta.JsonObjectOf("pollAt", pollAt))); err != nil {
				return nil, err
			}
			next := GenerationCheckpoint{Phase: generationPoll, Attempt: attempt, Compacted: compacted, Model: &ref, Cutoff: &cutoff, Handle: &handle, PollAt: &pollAt}
			return sleepingState[GenerationCheckpoint, GenerationResult](next, pollAt), nil
		})
	}
	if err := runtime.Hooks().Each("afterResponse", func(hooks *GenerationHooks) error {
		if hooks == nil || hooks.AfterResponse == nil {
			return nil
		}
		return hooks.AfterResponse(ctx, message, runtime)
	}); err != nil {
		return err
	}
	calls := toolCallsOf(message)
	if message.StopReason == ai.StopReasonToolUse && len(calls) > 0 {
		return startToolRound(ctx, runtime, request, message, calls)
	}
	if message.StopReason == ai.StopReasonStop || message.StopReason == ai.StopReasonLength || message.StopReason == ai.StopReasonToolUse {
		return answer(ctx, runtime, message)
	}
	// The retry and compaction policies govern the next attempt, so they are read now rather than pinned at
	// preparation.
	settings := runtime.Settings()
	compaction := settings.Compaction.PolicyFor(ref)
	overflow := message.StopReason == ai.StopReasonError && ai.IsContextOverflow(message, 0)
	if overflow && compacted == nil && compaction.Enabled {
		view, err := runtime.Context(ctx, conversationId, &durable.ContextOptions{At: &cutoff})
		if err != nil {
			return err
		}
		if _, ok := SelectCut(view, compaction.KeepRecentTokens); ok {
			text := message.ErrorMessage
			if text == "" {
				text = "Context overflow"
			}
			return runtime.Commit(ctx, func(tx durable.Tx, _ durable.RunningTask[GenerationInput, GenerationCheckpoint, GenerationResult]) (*generationNext, error) {
				live, err := docDraft(tx, LiveDoc, conversationId)
				if err != nil {
					return nil, err
				}
				if _, err := appendAssistant(tx, conversationId, message); err != nil {
					return nil, err
				}
				live.Delete("generation")
				owner := runtime.TaskId()
				child, err := CreateCompaction(tx, conversationId, CompactionInput{Reason: durable.CompactionOverflow}, &owner)
				if err != nil {
					return nil, err
				}
				next := GenerationCheckpoint{Phase: generationPrepare, Attempt: attempt, Compacted: &child, Overflow: &text}
				return waitingState[GenerationCheckpoint, GenerationResult](next, []durable.TaskId{child}, durable.JoinAllSettled), nil
			})
		}
	}
	policy := settings.Retry
	// An overflow is never retried: only a compaction can make the next request fit.
	retry := message.StopReason == ai.StopReasonError && !overflow && ai.IsRetryableAssistantError(message) && policy.Enabled && attempt <= policy.MaxRetries
	until := 0.0
	if retry {
		until = runtime.Now() + float64(ai.RetryDelayMs(ai.RetryPolicy{BaseDelayMs: policy.BaseDelayMs, MaxAgentDelayMs: policy.MaxAgentDelayMs}, attempt))
	}
	return runtime.Commit(ctx, func(tx durable.Tx, _ durable.RunningTask[GenerationInput, GenerationCheckpoint, GenerationResult]) (*generationNext, error) {
		// Read before the first table write; only a failure uses it.
		var failed failure
		if !retry {
			var err error
			if failed, err = prepareFailure(tx, runtime); err != nil {
				return nil, err
			}
		}
		live, err := docDraft(tx, LiveDoc, conversationId)
		if err != nil {
			return nil, err
		}
		if _, err := appendAssistant(tx, conversationId, message); err != nil {
			return nil, err
		}
		if retry {
			if err := live.Set("generation", delta.JsonObjectOf("attempt", float64(attempt), "retry", delta.JsonObjectOf("at", until, "error", message.ErrorMessage))); err != nil {
				return nil, err
			}
			next := GenerationCheckpoint{Phase: generationRetry, Attempt: attempt, Compacted: compacted, Until: &until}
			return sleepingState[GenerationCheckpoint, GenerationResult](next, until), nil
		}
		text := message.ErrorMessage
		if text == "" {
			text = fmt.Sprintf("Model response ended with stop reason %s", message.StopReason)
		}
		if err := endFailedRun(tx, runtime, live, failed, durable.SubmissionSettlement{Status: durable.SubmissionUnanswered, Reason: modelErrorReason, Detail: text}); err != nil {
			return nil, err
		}
		return failedState[GenerationCheckpoint, GenerationResult](text, modelErrorReason), nil
	})
}

// answer handles a final answer; the final boundary places queued items (spec §6, generation.ts:495-523). The first
// onYield continuation appends a user message and hands the run to a successor generation, but only when the boundary
// selected no user item and no reset. Otherwise the run's inputs settle done, and selected user items start the next
// run.
func answer(ctx context.Context, runtime generationRuntime, message ai.AssistantMessage) error {
	var continuation *YieldContinue
	if err := runtime.Hooks().Each("onYield", func(hooks *GenerationHooks) error {
		if continuation != nil || hooks == nil || hooks.OnYield == nil {
			return nil
		}
		result, err := hooks.OnYield(ctx, message, runtime)
		if err != nil {
			return err
		}
		continuation = result
		return nil
	}); err != nil {
		return err
	}
	conversationId := runtime.ConversationId()
	return runtime.Commit(ctx, func(tx durable.Tx, _ durable.RunningTask[GenerationInput, GenerationCheckpoint, GenerationResult]) (*generationNext, error) {
		// Queue modes are read on the Session line, when the boundary is decided.
		boundary, err := PrepareBoundary(tx, conversationId, QueueModesOf(runtime.Settings()))
		if err != nil {
			return nil, err
		}
		live, err := docDraft(tx, LiveDoc, conversationId)
		if err != nil {
			return nil, err
		}
		entry, err := appendAssistant(tx, conversationId, message)
		if err != nil {
			return nil, err
		}
		result := completedState[GenerationCheckpoint, GenerationResult](GenerationResult{EntryId: entry.Id})
		placed, err := ApplyBoundary(tx, boundary, BoundaryFinal, runtime.Now())
		if err != nil {
			return nil, err
		}
		if continuation != nil && len(placed.Users) == 0 && !placed.Reset {
			user := ai.UserMessage{Content: continuation.Continue, Timestamp: int64(runtime.Now())}
			if _, err := durable.TxAppendEntry(tx, durable.UserEntry, conversationId, durable.TypedEntryDraft[durable.Never]{Model: []ai.Message{user}}); err != nil {
				return nil, err
			}
			successor, err := createGeneration(tx, conversationId, nil)
			if err != nil {
				return nil, err
			}
			if err := handOver(live, runtime.TaskId(), successor); err != nil {
				return nil, err
			}
			live.Delete("generation")
			return result, nil
		}
		if err := EndRun(tx, live, runtime.TaskId(), durable.SubmissionSettlement{Status: durable.SubmissionDone, Answer: entry.Id}); err != nil {
			return nil, err
		}
		if len(placed.Users) > 0 {
			if err := StartRun(tx, conversationId, live, placed.Users); err != nil {
				return nil, err
			}
		}
		return result, nil
	})
}

// startToolRound appends the tool-calling answer and starts its tool round in one commit (spec §8.3,
// generation.ts:531-578). A call to a tool the request did not offer gets its tool_unavailable result here; every other
// call gets a tool task owned by the generation, only the first one now when the round is sequential. The generation
// then waits for them in its tools phase, keeping the run.
func startToolRound(ctx context.Context, runtime generationRuntime, request generationRequestInfo, message ai.AssistantMessage, calls []ai.ToolCall) error {
	conversationId := runtime.ConversationId()
	messages := request.messages
	if messages == nil {
		view, err := runtime.Context(ctx, conversationId, &durable.ContextOptions{At: &request.cutoff})
		if err != nil {
			return err
		}
		messages = view.Messages
	}
	offered := map[string]bool{}
	for _, tool := range ai.GetCurrentTools(messages) {
		offered[tool.Name] = true
	}
	// Read as the round starts; a tool is resolved as its tool task resolves it.
	agent, err := runtime.Agent(ctx)
	if err != nil {
		return err
	}
	sequential := runtime.Settings().ToolExecution == durable.ToolExecutionSequential
	for _, call := range calls {
		if !offered[call.Name] {
			continue
		}
		for _, tool := range agent.Tools {
			if tool.Name == call.Name {
				if tool.ExecutionMode == durable.ToolExecutionSequential {
					sequential = true
				}
				break
			}
		}
	}
	return runtime.Commit(ctx, func(tx durable.Tx, _ durable.RunningTask[GenerationInput, GenerationCheckpoint, GenerationResult]) (*generationNext, error) {
		live, err := docDraft(tx, LiveDoc, conversationId)
		if err != nil {
			return nil, err
		}
		entry, err := appendAssistant(tx, conversationId, message)
		if err != nil {
			return nil, err
		}
		slots := []ToolSlot{}
		tools := []durable.TaskId{}
		pending := []string{}
		for _, call := range calls {
			if !offered[call.Name] {
				unavailable := HarnessError(toolUnavailable, fmt.Sprintf("Tool %s is not available", call.Name))
				result, err := AppendToolResult(tx, conversationId, call, unavailable, runtime.Now(), nil)
				if err != nil {
					return nil, err
				}
				id := result.Id
				slots = append(slots, ToolSlot{CallId: call.ID, Name: call.Name, Status: ToolSlotDone, Entry: &id})
				continue
			}
			if sequential && len(tools) > 0 {
				pending = append(pending, call.ID)
				slots = append(slots, ToolSlot{CallId: call.ID, Name: call.Name, Status: ToolSlotPending})
				continue
			}
			// pig divergence (D177): the tool tasks of a parallel round run concurrently, so the order in which their calls
			// start within the round is unspecified; Pi's event loop runs each execute's synchronous prefix in call order.
			taskId, err := createToolTask(tx, runtime.TaskId(), entry.Id, call.ID)
			if err != nil {
				return nil, err
			}
			tools = append(tools, taskId)
			slots = append(slots, ToolSlot{CallId: call.ID, Name: call.Name, TaskId: &taskId, Status: ToolSlotPending})
		}
		live.Delete("generation")
		if err := setJSON(live, "tools", slots); err != nil {
			return nil, err
		}
		assistant := entry.Id
		next := GenerationCheckpoint{Phase: generationTools, Assistant: &assistant, Tools: tools, Pending: pending}
		return waitingState[GenerationCheckpoint, GenerationResult](next, tools, durable.JoinAllSettled), nil
	})
}

// finishToolRound applies the controls of a round whose tools are terminal and either ends the run at the final
// boundary (terminate, handoff, or a queued reset) or hands it to the next generation at the postTools boundary (spec
// §8.5, generation.ts:584-635).
func finishToolRound(ctx context.Context, runtime generationRuntime, assistant durable.EntryId, tools []durable.TaskId) error {
	conversationId := runtime.ConversationId()
	controls := make(map[durable.TaskId]*durable.ToolControl, len(tools))
	var ordered []*durable.ToolControl
	// pig divergence (D181): this read and the commits after it race the commits of a job started in the round and not awaited
	// (see TaskScheduler.start); their order against each other is unspecified, where Pi's microtask hops fix it.
	outcomes, err := runtime.Outcomes(ctx, tools)
	if err != nil {
		return err
	}
	for index, id := range tools {
		var control *durable.ToolControl
		if outcomes[index].Status == durable.OutcomeCompleted {
			if result, err := decodeOutcomeResult[ToolTaskResult](outcomes[index]); err == nil && result != nil {
				control = result.Control
			}
		}
		controls[id] = control
		ordered = append(ordered, control)
	}
	snapshot, err := durable.Snapshot[LiveState](ctx, runtime, LiveDoc, conversationId)
	if err != nil {
		return err
	}
	var slots []ToolSlot
	if snapshot != nil {
		slots = snapshot.Tools
	}
	results := []durable.EntryId{}
	for _, slot := range slots {
		if slot.Entry != nil {
			results = append(results, *slot.Entry)
		}
	}
	if err := runtime.Hooks().Each("afterTools", func(hooks *GenerationHooks) error {
		if hooks == nil || hooks.AfterTools == nil {
			return nil
		}
		return hooks.AfterTools(ctx, assistant, results, runtime)
	}); err != nil {
		return err
	}
	// Every call of the round, including those answered without a task, must ask to terminate.
	terminate := len(slots) > 0
	for _, slot := range slots {
		if slot.TaskId == nil {
			terminate = false
			break
		}
		control := controls[*slot.TaskId]
		if control == nil || !control.Terminate {
			terminate = false
			break
		}
	}
	var added []string
	var handoff *string
	for _, control := range ordered {
		if control == nil {
			continue
		}
		added = append(added, control.AddTools...)
		// The last handoff in call order wins.
		if control.Handoff != nil {
			handoff = control.Handoff
		}
	}
	return runtime.Commit(ctx, func(tx durable.Tx, _ durable.RunningTask[GenerationInput, GenerationCheckpoint, GenerationResult]) (*generationNext, error) {
		boundary, err := PrepareBoundary(tx, conversationId, QueueModesOf(runtime.Settings()))
		if err != nil {
			return nil, err
		}
		if len(added) > 0 {
			if err := AddTools(tx, conversationId, added); err != nil {
				return nil, err
			}
		}
		live, err := docDraft(tx, LiveDoc, conversationId)
		if err != nil {
			return nil, err
		}
		now := runtime.Now()
		taskId := runtime.TaskId()
		if terminate || handoff != nil {
			if handoff != nil {
				message := ai.UserMessage{Content: ai.UserText(*handoff), Timestamp: int64(now)}
				entry, err := durable.TxAppendEntry(tx, durable.ResetEntry, conversationId, durable.TypedEntryDraft[durable.Never]{HeadSelf: true, Model: []ai.Message{message}})
				if err != nil {
					return nil, err
				}
				head := entry.Id
				boundary.Head = &head
			}
			placed, err := ApplyBoundary(tx, boundary, BoundaryFinal, now)
			if err != nil {
				return nil, err
			}
			if err := EndRun(tx, live, taskId, durable.SubmissionSettlement{Status: durable.SubmissionDone, Answer: assistant}); err != nil {
				return nil, err
			}
			if len(placed.Users) > 0 {
				if err := StartRun(tx, conversationId, live, placed.Users); err != nil {
					return nil, err
				}
			}
		} else {
			placed, err := ApplyBoundary(tx, boundary, BoundaryPostTools, now)
			if err != nil {
				return nil, err
			}
			if placed.Reset {
				// The queued reset cut the run's context before an answer.
				if err := EndRun(tx, live, taskId, durable.SubmissionSettlement{Status: durable.SubmissionUnanswered, Reason: "reset"}); err != nil {
					return nil, err
				}
				if len(placed.Users) > 0 {
					if err := StartRun(tx, conversationId, live, placed.Users); err != nil {
						return nil, err
					}
				}
			} else {
				live.Delete("tools")
				live.Delete("nestedTools")
				if owner, ok := runTaskOf(live); ok && owner == taskId && len(placed.Users) > 0 {
					inputs := live.Object("run").Array("inputs")
					values := make([]any, len(placed.Users))
					for index, id := range placed.Users {
						values[index] = float64(id)
					}
					if _, err := inputs.Push(values...); err != nil {
						return nil, err
					}
				}
				successor, err := createGeneration(tx, conversationId, nil)
				if err != nil {
					return nil, err
				}
				if err := handOver(live, taskId, successor); err != nil {
					return nil, err
				}
			}
		}
		return completedState[GenerationCheckpoint, GenerationResult](GenerationResult{EntryId: assistant}), nil
	})
}

// appendAssistant appends a provider result and adds its usage to pi.usage in the same commit. Every built-in writer
// of assistant entries goes through here, so the usage ledger stays complete (generation.ts:641-648).
func appendAssistant(tx durable.Tx, conversationId durable.ConversationId, message ai.AssistantMessage) (*durable.TypedEntry[durable.Never], error) {
	if err := RecordUsage(tx, conversationId, UsageModels, message.Provider+"/"+message.Model, message.Usage); err != nil {
		return nil, err
	}
	return durable.TxAppendEntry(tx, durable.AssistantEntry, conversationId, durable.TypedEntryDraft[durable.Never]{Model: []ai.Message{message}})
}

// StartRun starts a run for inputs, placed input submissions: a new generation takes pi.live.run. The generation starts
// in its first input's trace and links to the others.
func StartRun(tx durable.Tx, conversationId durable.ConversationId, live *delta.Object, inputs []durable.SubmissionId) error {
	taskId, err := createGeneration(tx, conversationId, nonNil(inputs))
	if err != nil {
		return err
	}
	return setJSON(live, "run", LiveRun{TaskId: taskId, Inputs: nonNil(inputs)})
}

// submissionTaskCreator is the Session transaction's internal creation entry, which resolves the carriers of the
// submissions a run starts for (transaction.ts TaskCreationOptions.submissionIds).
type submissionTaskCreator interface {
	CreateTaskFromSubmissions(task durable.AnyTask, input durable.JsonValue, options durable.TaskOptions, submissionIds []durable.SubmissionId) (durable.TaskId, error)
}

// createGeneration creates a generation owned by its conversation; submissionIds, when not nil, are the inputs a new run
// starts for.
func createGeneration(tx durable.Tx, conversationId durable.ConversationId, submissionIds []durable.SubmissionId) (durable.TaskId, error) {
	id := conversationId
	options := durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}, ConversationId: &id}
	creator, ok := tx.(submissionTaskCreator)
	if !ok || submissionIds == nil {
		return durable.CreateTask(tx, GenerationTask, GenerationInput{}, options)
	}
	encoded, err := durable.ToJsonValue(GenerationInput{})
	if err != nil {
		return 0, err
	}
	return creator.CreateTaskFromSubmissions(GenerationTask.Erased(), encoded, options, submissionIds)
}

// handOver hands run control from one task to another; the run's inputs move with it.
func handOver(live *delta.Object, from, to durable.TaskId) error {
	if owner, ok := runTaskOf(live); ok && owner == from {
		return live.Object("run").Set("taskId", float64(to))
	}
	return nil
}

// streamOptionsOf builds the pi-ai request options from the pinned stream options.
func streamOptionsOf(stream durable.ConversationStreamOptions, signal context.Context, thinking ai.ModelThinkingLevel) ai.StreamOptions {
	options := ai.StreamOptions{
		Transport:       stream.Transport,
		TimeoutMs:       stream.TimeoutMs,
		MaxRetries:      stream.MaxRetries,
		MaxRetryDelayMs: stream.MaxRetryDelayMs,
		Metadata:        stream.Metadata,
		CacheRetention:  stream.CacheRetention,
		Deferred:        stream.Deferred,
		Signal:          signal,
	}
	if stream.Headers != nil {
		options.Headers = ai.ProviderHeaders{}
		for name, value := range stream.Headers {
			options.Headers[name] = &value
		}
	}
	options.Thinking = thinking.ReasoningOption()
	return options
}

// decodeOutcomeResult decodes the result of a completed outcome; nil without one.
func decodeOutcomeResult[R any](outcome durable.TaskOutcome[durable.JsonValue]) (*R, error) {
	if outcome.Result == nil {
		return nil, nil
	}
	decoded, err := durable.FromJsonValue[R](*outcome.Result)
	if err != nil {
		return nil, err
	}
	return &decoded, nil
}

// compactionFailure is the error text for a generation whose blocking compaction ended without completing
// (generation.ts compactionFailure).
func compactionFailure(outcome durable.TaskOutcome[durable.JsonValue]) string {
	switch outcome.Status {
	case durable.OutcomeFailed, durable.OutcomeFaulted:
		if outcome.Error != nil { // the outcome contract requires Error for these statuses
			return "Compaction failed: " + outcome.Error.Message
		}
	case durable.OutcomeOrphaned:
		if outcome.Reason != nil { // the outcome contract requires Reason for orphaned
			return "Compaction failed: " + *outcome.Reason
		}
	}
	return "Compaction was aborted"
}

func nonNil[T any](items []T) []T {
	if items == nil {
		return []T{}
	}
	return items
}
