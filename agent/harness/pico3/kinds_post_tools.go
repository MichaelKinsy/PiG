package pico3

import (
	"context"
	"fmt"
	"slices"

	"github.com/MichaelKinsy/PiG/ai"
)

// PostToolsHooks are the hook points pi.post_tools calls.
type PostToolsHooks struct {
	// AfterTools observes the batch's result entries.
	AfterTools func(ctx context.Context, assistant Id, results []Id, info HookApi) error
}

func postToolsHooksOf(handlers any) *PostToolsHooks {
	switch typed := handlers.(type) {
	case *PostToolsHooks:
		return typed
	case PostToolsHooks:
		return &typed
	default:
		return nil
	}
}

type postToolsInput struct {
	Inputs    []Id `json:"inputs"`
	Assistant Id   `json:"assistant"`
	Tools     []Id `json:"tools"`
}

func postToolsInputOf(task Task) postToolsInput {
	var input postToolsInput
	_ = decodeInto(task.Input, &input)
	return input
}

var postToolsConfig = &KindConfig{
	Sticky: []ConfigKey{
		{Key: "steeringMode", Default: "one-at-a-time"},
		{Key: "followUpMode", Default: "one-at-a-time"},
	},
}

// postToolsKind closes a tool batch: it applies tool controls, synthesizes
// missing results, and either ends the turn or starts the next generation.
var postToolsKind = &Kind{
	Name:    "pi.post_tools",
	Turn:    true,
	Config:  postToolsConfig,
	Phases:  map[string]PhaseHandler{},
	Initial: postToolsInitial,
	Abort: func(_ context.Context, task Task, _ *Runtime) (AbortClosure, error) {
		inputs := postToolsInputOf(task).Inputs
		return func(_ context.Context, tx *Tx, current Task) (JsonValue, error) {
			if err := resetTurn(tx, current.ConversationId); err != nil {
				return nil, err
			}
			if err := tx.ResolveInputs(inputs, InputResolution{Status: InputUnanswered, Reason: "aborted"}); err != nil {
				return nil, err
			}
			return nil, emit(tx, ViewEvent{"type": "turn.ended", "inputs": idsJSON(inputs), "status": "unanswered", "reason": "aborted"})
		}, nil
	},
}

// toolRow is one tool task's settled outcome.
type toolRow struct {
	call    JsonObject
	entry   *Id
	control *ToolControl
	missing string
}

func postToolsInitial(ctx context.Context, task Task, rt *Runtime) (Step, error) {
	input := postToolsInputOf(task)
	rows, err := CommitAs(ctx, rt, func(_ context.Context, tx *Tx, _ Task) ([]toolRow, error) {
		return collectToolRows(tx, input)
	})
	if err != nil {
		return Step{}, err
	}
	var results []Id
	for _, row := range rows {
		if row.entry != nil {
			results = append(results, *row.entry)
		}
	}
	err = rt.Hooks.Each(ctx, func(handlers any, api HookApi) (any, error) {
		if hooks := postToolsHooksOf(handlers); hooks != nil && hooks.AfterTools != nil {
			return nil, hooks.AfterTools(ctx, input.Assistant, append([]Id{}, results...), api)
		}
		return nil, nil
	}, nil)
	if err != nil {
		return Step{}, err
	}
	return Step{Done: func(_ context.Context, tx *Tx, current Task) (Completion, error) {
		return closePostTools(tx, current, input, rows, rt)
	}}, nil
}

func collectToolRows(tx *Tx, input postToolsInput) ([]toolRow, error) {
	assistant, err := tx.Entry(input.Assistant)
	if err != nil {
		return nil, err
	}
	var calls []JsonObject
	if assistant != nil && len(assistant.Model) > 0 {
		for _, block := range arr(assistant.Model[0], "content") {
			if content, ok := block.(map[string]any); ok && content["type"] == "toolCall" {
				calls = append(calls, content)
			}
		}
	}
	rows := make([]toolRow, len(input.Tools))
	for index, id := range input.Tools {
		toolTask, err := tx.Task(id)
		if err != nil {
			return nil, err
		}
		if toolTask == nil {
			return nil, fmt.Errorf("tool task %d not found", id)
		}
		rows[index] = rowOf(*toolTask, indexOf(anySlice(calls), index))
	}
	return rows, nil
}

func anySlice(objects []JsonObject) []any {
	out := make([]any, len(objects))
	for index, object := range objects {
		out[index] = object
	}
	return out
}

func rowOf(toolTask Task, callValue any) toolRow {
	call, _ := callValue.(map[string]any)
	row := toolRow{call: call, missing: "orphaned"}
	if toolTask.Outcome == nil {
		return row
	}
	result := asObject(toolTask.Outcome.Result)
	switch toolTask.Outcome.Status {
	case OutcomeCompleted:
		row.missing = ""
		if id, ok := asID(result["entry"]); ok {
			row.entry = &id
		}
		if control, ok := result["control"].(map[string]any); ok {
			row.control = &ToolControl{}
			_ = decodeInto(control, row.control)
		}
	case OutcomeAborted:
		if id, ok := asID(result["entry"]); ok {
			row.entry = &id
			row.missing = ""
		} else {
			row.missing = "aborted"
		}
	}
	return row
}

func closePostTools(tx *Tx, current Task, input postToolsInput, rows []toolRow, rt *Runtime) (Completion, error) {
	conversationId := current.ConversationId
	head, err := tx.NewestEntry(conversationId, NewestOptions{WithHead: true})
	if err != nil {
		return Completion{}, err
	}
	terminate, handoff, err := applyToolControls(tx, conversationId, rows)
	if err != nil {
		return Completion{}, err
	}
	if err := synthesizeMissing(tx, conversationId, rows, rt.Now()); err != nil {
		return Completion{}, err
	}
	headBoundary := entryIdOf(head)
	if handoff != nil {
		id, err := tx.AppendEntry(conversationId, NewEntry{Kind: "pi.handoff", Head: SelfHead(), Model: []JsonObject{userMessage(*handoff, rt.Now())}})
		if err != nil {
			return Completion{}, err
		}
		headBoundary = &id
	}
	if handoff != nil || terminate {
		ended := "terminate"
		if handoff != nil {
			ended = "handoff"
		}
		return endTurnByControl(tx, conversationId, input, headBoundary, ended)
	}
	return continueAfterTools(tx, conversationId, input, headBoundary)
}

// applyToolControls merges added tools into selectedTools and reports
// terminate and the last handoff.
func applyToolControls(tx *Tx, conversationId Id, rows []toolRow) (bool, *string, error) {
	state, err := coreRewindable(tx, conversationId)
	if err != nil {
		return false, nil, err
	}
	selected := stringList(state["selectedTools"])
	count := len(selected)
	terminate := false
	var handoff *string
	for _, row := range rows {
		if row.control == nil {
			continue
		}
		for _, name := range row.control.AddTools {
			if !slices.Contains(selected, name) {
				selected = append(selected, name)
			}
		}
		if row.control.Terminate {
			terminate = true
		}
		if row.control.Handoff != nil {
			handoff = row.control.Handoff
		}
	}
	if len(selected) != count {
		state["selectedTools"] = mustStored(selected)
		if err := emit(tx, ViewEvent{"type": "config.changed", "keys": []any{"selectedTools"}}); err != nil {
			return false, nil, err
		}
	}
	return terminate, handoff, nil
}

func synthesizeMissing(tx *Tx, conversationId Id, rows []toolRow, now float64) error {
	for _, row := range rows {
		if row.missing == "" {
			continue
		}
		message := JsonObject{
			"role":       "toolResult",
			"toolCallId": str(row.call, "id"),
			"toolName":   str(row.call, "name"),
			"content":    mustStored([]ai.ToolResultMessageContent{ai.TextContent{Text: fmt.Sprintf("Tool result unavailable: task %s.", row.missing)}}),
			"isError":    true,
			"timestamp":  now,
		}
		if _, err := tx.AppendEntry(conversationId, NewEntry{
			Kind:  "pi.tool_result",
			Model: []JsonObject{message},
			Data:  JsonObject{"diagnostics": []any{JsonObject{"severity": "error", "message": row.missing, "code": row.missing}}},
		}); err != nil {
			return err
		}
		if err := emit(tx, ViewEvent{"type": "warning", "source": "post_tools", "message": fmt.Sprintf("synthesized %s tool result for %s", row.missing, str(row.call, "id"))}); err != nil {
			return err
		}
	}
	return nil
}

func endTurnByControl(tx *Tx, conversationId Id, input postToolsInput, headBoundary *Id, ended string) (Completion, error) {
	if err := resetTurn(tx, conversationId); err != nil {
		return Completion{}, err
	}
	if err := tx.ResolveInputs(input.Inputs, InputResolution{Status: InputDone, Answer: input.Assistant}); err != nil {
		return Completion{}, err
	}
	if err := emit(tx, ViewEvent{"type": "turn.ended", "inputs": idsJSON(input.Inputs), "status": "done", "answer": float64(input.Assistant)}); err != nil {
		return Completion{}, err
	}
	boundary, err := tx.Boundary(conversationId, "final", headBoundary)
	if err != nil {
		return Completion{}, err
	}
	result := JsonObject{"ended": ended}
	if err := startSuccessor(tx, boundary.Triggers, result); err != nil {
		return Completion{}, err
	}
	return Completed(result), nil
}

// startSuccessor announces and creates the successor generation for triggers.
func startSuccessor(tx *Tx, triggers []Id, result JsonObject) error {
	if len(triggers) == 0 {
		return nil
	}
	if err := emit(tx, ViewEvent{"type": "turn.started", "inputs": idsJSON(triggers)}); err != nil {
		return err
	}
	successor, err := tx.CreateTaskSpec(TaskSpec{Kind: "pi.generation", Input: JsonObject{"inputs": idsJSON(triggers)}})
	if err != nil {
		return err
	}
	result["successor"] = float64(successor)
	return nil
}

func continueAfterTools(tx *Tx, conversationId Id, input postToolsInput, headBoundary *Id) (Completion, error) {
	boundary, err := tx.Boundary(conversationId, "postTools", headBoundary)
	if err != nil {
		return Completion{}, err
	}
	if boundary.Terminated {
		if err := resetTurn(tx, conversationId); err != nil {
			return Completion{}, err
		}
		if err := tx.ResolveInputs(input.Inputs, InputResolution{Status: InputUnanswered, Reason: "terminated"}); err != nil {
			return Completion{}, err
		}
		if err := emit(tx, ViewEvent{"type": "turn.ended", "inputs": idsJSON(input.Inputs), "status": "unanswered", "reason": "terminated"}); err != nil {
			return Completion{}, err
		}
		result := JsonObject{}
		if err := startSuccessor(tx, boundary.Triggers, result); err != nil {
			return Completion{}, err
		}
		return Completed(result), nil
	}
	inputs := append(append([]Id{}, input.Inputs...), boundary.Triggers...)
	successor, err := tx.CreateTaskSpec(TaskSpec{Kind: "pi.generation", Input: JsonObject{"inputs": idsJSON(inputs)}})
	if err != nil {
		return Completion{}, err
	}
	return Completed(JsonObject{"successor": float64(successor)}), nil
}
