package pico3

import (
	"context"
	"fmt"

	"github.com/MichaelKinsy/PiG/ai"
)

func providerError(message ai.AssistantMessage) string {
	if message.ErrorMessage != "" {
		return message.ErrorMessage
	}
	return "provider error"
}

// classify handles one terminal provider message (steps 3 to 5).
func classify(ctx context.Context, task Task, prep generationPrep, message ai.AssistantMessage, rt *Runtime) (Step, error) {
	if err := callAfterResponse(ctx, rt, message, prep.Attempt); err != nil {
		return Step{}, err
	}
	switch message.StopReason {
	case ai.StopReasonError:
		decision := retryDecision(prep.Retry, prep.Attempt, &message, rt.Now())
		if decision.retry {
			return retryStep(prep, message, decision.untilMs), nil
		}
		return Step{Done: terminalError(task, prep, message, decision.reason)}, nil
	case ai.StopReasonAborted:
		return Step{Done: terminalError(task, prep, message, "provider")}, nil
	}
	var calls []ai.ToolCall
	for _, block := range message.Content {
		if call, ok := block.(ai.ToolCall); ok {
			calls = append(calls, call)
		}
	}
	if len(calls) > 0 {
		return Step{Done: toolCallsClosure(task, prep, message, calls)}, nil
	}
	continueText, err := yieldContinuation(ctx, rt, message)
	if err != nil {
		return Step{}, err
	}
	return Step{Done: finalAnswerClosure(task, prep, message, continueText, rt)}, nil
}

func callAfterResponse(ctx context.Context, rt *Runtime, message ai.AssistantMessage, attempt int) error {
	return rt.Hooks.Each(ctx, func(handlers any, api HookApi) (any, error) {
		if hooks := generationHooksOf(handlers); hooks != nil && hooks.AfterResponse != nil {
			return nil, hooks.AfterResponse(ctx, message, AfterResponseInfo{HookApi: api, Attempt: attempt})
		}
		return nil, nil
	}, nil)
}

func yieldContinuation(ctx context.Context, rt *Runtime, message ai.AssistantMessage) (*string, error) {
	var continueText *string
	err := rt.Hooks.Each(ctx, func(handlers any, api HookApi) (any, error) {
		hooks := generationHooksOf(handlers)
		if hooks == nil || hooks.OnYield == nil {
			return nil, nil
		}
		result, err := hooks.OnYield(ctx, message, api)
		if result == nil {
			return nil, err
		}
		return result, err
	}, func(value any) bool {
		text := value.(*YieldResult).Continue
		continueText = &text
		return true
	})
	return continueText, err
}

func retryStep(prep generationPrep, message ai.AssistantMessage, untilMs float64) Step {
	errorText := providerError(message)
	return Step{Build: func(_ context.Context, tx *Tx, current Task) (Transition, error) {
		usage := mustStored(message.Usage)
		if _, err := tx.AppendEntry(current.ConversationId, NewEntry{Kind: "pi.usage", Data: JsonObject{"attempt": prep.Attempt, "usage": usage, "error": errorText}}); err != nil {
			return Transition{}, err
		}
		return retryingTransition(tx, current, prep, untilMs, errorText)
	}}
}

// toolCallsClosure appends the assistant, seeds the tool slots, and creates
// one tool task per call plus post_tools.
func toolCallsClosure(task Task, prep generationPrep, message ai.AssistantMessage, calls []ai.ToolCall) Closure {
	stored := storedMessage(message)
	return func(_ context.Context, tx *Tx, current Task) (Completion, error) {
		conversationId := current.ConversationId
		collapseThrough, err := thresholdCollapseThrough(tx, conversationId, message)
		if err != nil {
			return Completion{}, err
		}
		assistant, err := tx.AppendEntry(conversationId, NewEntry{Kind: "pi.assistant", Model: []JsonObject{stored}, Data: JsonObject{"attempt": prep.Attempt}})
		if err != nil {
			return Completion{}, err
		}
		tools, err := seedToolTasks(tx, conversationId, assistant, prep, calls)
		if err != nil {
			return Completion{}, err
		}
		postTools, err := tx.CreateTaskSpec(TaskSpec{Kind: "pi.post_tools", After: tools, Input: JsonObject{"inputs": idsJSON(generationInputs(task)), "assistant": float64(assistant), "tools": idsJSON(tools)}})
		if err != nil {
			return Completion{}, err
		}
		if err := createThresholdCollapse(tx, collapseThrough); err != nil {
			return Completion{}, err
		}
		if err := emit(tx, ViewEvent{"type": "generation.completed", "taskId": float64(current.Id), "entry": float64(assistant), "toolCalls": len(calls)}); err != nil {
			return Completion{}, err
		}
		return Completed(JsonObject{"assistant": float64(assistant), "tools": idsJSON(tools), "postTools": float64(postTools)}), nil
	}
}

func seedToolTasks(tx *Tx, conversationId, assistant Id, prep generationPrep, calls []ai.ToolCall) ([]Id, error) {
	turn, err := turnOf(tx, conversationId)
	if err != nil {
		return nil, err
	}
	delete(turn, "message")
	slots := make([]any, len(calls))
	for index, call := range calls {
		slots[index] = JsonObject{"callId": call.ID, "name": call.Name, "args": mustStored(map[string]any(call.Arguments)), "status": "pending"}
	}
	turn["tools"] = slots
	offered := make([]any, len(prep.Tools))
	for index, name := range prep.Tools {
		offered[index] = name
	}
	tools := make([]Id, 0, len(calls))
	for index, call := range calls {
		id, err := tx.CreateTaskSpec(TaskSpec{Kind: "pi.tool", Input: JsonObject{"assistant": float64(assistant), "call": storedToolCall(call), "offered": offered, "index": index}})
		if err != nil {
			return nil, err
		}
		tools = append(tools, id)
	}
	return tools, nil
}

func storedToolCall(call ai.ToolCall) JsonObject {
	if call.Arguments == nil {
		call.Arguments = ai.JsonObject{}
	}
	return storedObject(call)
}

func createThresholdCollapse(tx *Tx, through *Id) error {
	if through == nil {
		return nil
	}
	_, err := tx.CreateTaskSpec(TaskSpec{Kind: "pi.collapse", Input: JsonObject{"reason": "threshold", "through": float64(*through)}})
	return err
}

// finalAnswerClosure appends the answer, places queued input at the final
// boundary, and either continues, answers, or starts a successor group.
func finalAnswerClosure(task Task, prep generationPrep, message ai.AssistantMessage, continueText *string, rt *Runtime) Closure {
	stored := storedMessage(message)
	inputs := generationInputs(task)
	return func(_ context.Context, tx *Tx, current Task) (Completion, error) {
		conversationId := current.ConversationId
		collapseThrough, err := thresholdCollapseThrough(tx, conversationId, message)
		if err != nil {
			return Completion{}, err
		}
		head, err := tx.NewestEntry(conversationId, NewestOptions{WithHead: true})
		if err != nil {
			return Completion{}, err
		}
		assistant, err := tx.AppendEntry(conversationId, NewEntry{Kind: "pi.assistant", Model: []JsonObject{stored}, Data: JsonObject{"attempt": prep.Attempt}})
		if err != nil {
			return Completion{}, err
		}
		if err := emit(tx, ViewEvent{"type": "generation.completed", "taskId": float64(current.Id), "entry": float64(assistant), "toolCalls": 0}); err != nil {
			return Completion{}, err
		}
		if err := resetTurn(tx, conversationId); err != nil {
			return Completion{}, err
		}
		if err := createThresholdCollapse(tx, collapseThrough); err != nil {
			return Completion{}, err
		}
		boundary, err := tx.Boundary(conversationId, "final", entryIdOf(head))
		if err != nil {
			return Completion{}, err
		}
		if continueText != nil && len(boundary.Triggers) == 0 && !boundary.Terminated {
			return continueTurn(tx, conversationId, assistant, *continueText, inputs, rt)
		}
		return answerTurn(tx, conversationId, assistant, inputs, boundary.Triggers)
	}
}

func continueTurn(tx *Tx, conversationId, assistant Id, text string, inputs []Id, rt *Runtime) (Completion, error) {
	if _, err := tx.AppendEntry(conversationId, NewEntry{Kind: "pi.user", Model: []JsonObject{userMessage(text, rt.Now())}, Data: JsonObject{"continuation": true, "from": float64(assistant)}}); err != nil {
		return Completion{}, err
	}
	successor, err := tx.CreateTaskSpec(TaskSpec{Kind: "pi.generation", Input: JsonObject{"inputs": idsJSON(inputs)}})
	if err != nil {
		return Completion{}, err
	}
	return Completed(JsonObject{"assistant": float64(assistant), "tools": []any{}, "successor": float64(successor)}), nil
}

func answerTurn(tx *Tx, conversationId, assistant Id, inputs, triggers []Id) (Completion, error) {
	if err := tx.ResolveInputs(inputs, InputResolution{Status: InputDone, Answer: assistant}); err != nil {
		return Completion{}, err
	}
	if err := emit(tx, ViewEvent{"type": "turn.ended", "inputs": idsJSON(inputs), "status": "done", "answer": float64(assistant)}); err != nil {
		return Completion{}, err
	}
	result := JsonObject{"assistant": float64(assistant), "tools": []any{}}
	if len(triggers) > 0 {
		successor, err := tx.CreateTaskSpec(TaskSpec{Kind: "pi.generation", Input: JsonObject{"inputs": idsJSON(triggers)}})
		if err != nil {
			return Completion{}, err
		}
		if err := emit(tx, ViewEvent{"type": "turn.started", "inputs": idsJSON(triggers)}); err != nil {
			return Completion{}, err
		}
		result["successor"] = float64(successor)
	}
	_ = conversationId
	return Completed(result), nil
}

// terminalError appends a display-only assistant entry (no model) and leaves
// the inputs unanswered.
func terminalError(task Task, prep generationPrep, message ai.AssistantMessage, reason string) Closure {
	detail := providerError(message)
	return func(_ context.Context, tx *Tx, current Task) (Completion, error) {
		head, err := tx.NewestEntry(current.ConversationId, NewestOptions{WithHead: true})
		if err != nil {
			return Completion{}, err
		}
		displayReason := "error"
		if message.StopReason == ai.StopReasonAborted {
			displayReason = "aborted"
		}
		assistant, err := tx.AppendEntry(current.ConversationId, NewEntry{Kind: "pi.assistant", Data: JsonObject{"attempt": prep.Attempt, "display": storedMessage(message), "reason": displayReason}})
		if err != nil {
			return Completion{}, err
		}
		if err := emit(tx, ViewEvent{"type": "generation.failed", "taskId": float64(current.Id), "reason": reason, "detail": detail, "entry": float64(assistant)}); err != nil {
			return Completion{}, err
		}
		if err := settleFailedTurn(tx, current, generationInputs(task), detail, entryIdOf(head)); err != nil {
			return Completion{}, err
		}
		return generationFailure(reason, detail, &assistant), nil
	}
}

// thresholdCollapseThrough is read before the assistant is appended and
// applied after.
func thresholdCollapseThrough(tx *Tx, conversationId Id, message ai.AssistantMessage) (*Id, error) {
	collapses, err := tx.Tasks(TaskScan{ConversationId: &conversationId, Kind: "pi.collapse", Status: []string{TaskPending, TaskRunning}})
	if err != nil || len(collapses) > 0 {
		return nil, err
	}
	state, err := coreRewindable(tx, conversationId)
	if err != nil {
		return nil, err
	}
	threshold := numberOr(state["threshold"], 0)
	if threshold <= 0 {
		return nil, nil
	}
	view, err := tx.Context(conversationId, nil)
	if err != nil {
		return nil, err
	}
	used := float64(max(message.Usage.Input+message.Usage.Output, estimateTokens(append(view.Messages, storedMessage(message)), true)))
	if used <= threshold {
		return nil, nil
	}
	return chooseThrough(view.Entries, numberOr(state["keepRecent"], 0)), nil
}

// overflowStep collapses and replaces the generation, or fails when nothing
// is collapsible.
func overflowStep(task Task, rt *Runtime) Step {
	inputs := generationInputs(task)
	return Step{Done: func(_ context.Context, tx *Tx, current Task) (Completion, error) {
		conversationId := current.ConversationId
		state, err := coreRewindable(tx, conversationId)
		if err != nil {
			return Completion{}, err
		}
		view, err := tx.Context(conversationId, nil)
		if err != nil {
			return Completion{}, err
		}
		through := chooseThrough(view.Entries, numberOr(state["keepRecent"], 0))
		if err := resetTurn(tx, conversationId); err != nil {
			return Completion{}, err
		}
		if through == nil {
			return overflowFailure(tx, current, inputs, entryIdOf(view.Head), rt)
		}
		collapse, err := tx.CreateTaskSpec(TaskSpec{Kind: "pi.collapse", Input: JsonObject{"reason": "overflow", "through": float64(*through)}})
		if err != nil {
			return Completion{}, err
		}
		if _, err := tx.CreateTaskSpec(TaskSpec{Kind: "pi.generation", After: []Id{collapse}, Input: JsonObject{"inputs": idsJSON(inputs)}}); err != nil {
			return Completion{}, err
		}
		detail := fmt.Sprintf("collapsing through %d", *through)
		if err := emit(tx, ViewEvent{"type": "generation.failed", "taskId": float64(current.Id), "reason": "overflow", "detail": detail}); err != nil {
			return Completion{}, err
		}
		return generationFailure("overflow", detail, nil), nil
	}}
}

func overflowFailure(tx *Tx, current Task, inputs []Id, head *Id, rt *Runtime) (Completion, error) {
	const detail = "request exceeds context window and nothing is collapsible"
	if _, err := tx.Write(current.ConversationId, NewEntry{Kind: "pi.notice", Model: []JsonObject{userMessage("Context too large; nothing to compact.", rt.Now())}}); err != nil {
		return Completion{}, err
	}
	if err := emit(tx, ViewEvent{"type": "generation.failed", "taskId": float64(current.Id), "reason": "overflow", "detail": detail}); err != nil {
		return Completion{}, err
	}
	if err := settleFailedTurn(tx, current, inputs, "overflow", head); err != nil {
		return Completion{}, err
	}
	return generationFailure("overflow", detail, nil), nil
}
