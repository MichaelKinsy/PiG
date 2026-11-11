// SPDX-License-Identifier: MIT

package driver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/abi/payload"
	"github.com/MichaelKinsy/PiG/durable/core/sqlhost"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// hookEffect is the payload of a hook effect (host/PROTOCOL.md): one handler of one hook, run once.
type hookEffect struct {
	Name           string          `json:"name"`
	Handler        int             `json:"handler"`
	TaskID         int64           `json:"taskId"`
	ConversationID int64           `json:"conversationId"`
	Payload        json.RawMessage `json:"payload"`
}

// PrepareArguments is a tool's argument repair (durable.ToolRegistration.PrepareArguments).
type PrepareArguments func(args any) (any, error)

// Hooks runs hook effects against the handlers an extension registered.
type Hooks struct {
	// Lookup returns what the handler index names: a *harness.GenerationHooks, *harness.ToolHooks,
	// *harness.CompactionHooks or a PrepareArguments. The index is the host's append-only handler table, so an index
	// never changes meaning.
	Lookup func(handler int) (any, bool)
	// API returns the reads and memos a hook of one task may use.
	API func(task durable.TaskId, conversation durable.ConversationId) harness.HookApi
	// Models returns the Harness's models, which resolve the model a beforeProviderRequest hook receives.
	Models func() durable.Models
	// Task returns the registered definition of a task kind, for the `migrate` hook the core asks when it reserves a record that an
	// older version stored (scheduler.ts #resolve).
	Task func(kind string) (durable.AnyTask, bool)
}

// Handler returns the sqlhost handler of hook effects.
func (h *Hooks) Handler() sqlhost.Handler { return h.run }

func (h *Hooks) run(ctx context.Context, call *sqlhost.Call) error {
	var p hookEffect
	if err := json.Unmarshal(call.Payload, &p); err != nil {
		return fmt.Errorf("hook payload: %w", err)
	}
	if p.Name == "migrate" {
		return h.migrate(call, p)
	}
	if p.Name == "afterRun" {
		return h.afterRun(ctx, call, p)
	}
	target, ok := h.Lookup(p.Handler)
	if !ok {
		return fmt.Errorf("hook effect %d names handler %d, which the registry does not hold", call.ID, p.Handler)
	}
	api := h.API(durable.TaskId(p.TaskID), durable.ConversationId(p.ConversationID))
	var value any
	err := callDefinition(func() (err error) { value, err = runHook(ctx, p, target, api, h.Models); return err })
	if _, ok := errors.AsType[*protocolError](err); ok {
		return err
	}
	if err != nil {
		call.Post(abi.Event{Kind: abi.EventHookDone, ID: call.ID, Phase: abi.OutcomeThrown, Payload: wireError(err)})
		return nil
	}
	body, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("hook %s result: %w", p.Name, err)
	}
	call.Post(abi.Event{Kind: abi.EventHookDone, ID: call.ID, Phase: abi.OutcomeResult, Payload: body})
	return nil
}

// migrate runs a custom task's migrate(input, checkpoint, from): the answer is {input, checkpoint}; a throw is the task's failed
// migration, which the scheduler records and inspect reports (scheduler.ts #resolve).
func (h *Hooks) migrate(call *sqlhost.Call, p hookEffect) error {
	var in struct {
		Kind       string            `json:"kind"`
		Input      durable.JsonValue `json:"input"`
		Checkpoint durable.JsonValue `json:"checkpoint"`
		From       int               `json:"from"`
	}
	if err := json.Unmarshal(p.Payload, &in); err != nil {
		return protocol(p.Name, err)
	}
	var task durable.AnyTask
	if h.Task != nil {
		task, _ = h.Task(in.Kind)
	}
	var definition *durable.AnyTaskDefinition
	if task.Definition != nil {
		definition = task.AnyDefinition()
	}
	var input, checkpoint durable.JsonValue
	err := callDefinition(func() error {
		if definition == nil || definition.Migrate == nil {
			version := 1
			if definition != nil {
				version = definition.Version
			}
			return fmt.Errorf("Task %s version %d has no migration from %d", in.Kind, version, in.From)
		}
		var err error
		input, checkpoint, err = definition.Migrate(in.Input, in.Checkpoint, in.From)
		return err
	})
	if err != nil {
		call.Post(abi.Event{Kind: abi.EventHookDone, ID: call.ID, Phase: abi.OutcomeThrown, Payload: wireError(err)})
		return nil
	}
	//portlint:allow mapkeyorder driver-to-core wire (durable/core/host/PROTOCOL.md): the core reads its members by name and stores none of this object
	body, err := json.Marshal(map[string]any{"input": input, "checkpoint": checkpoint})
	if err != nil {
		return fmt.Errorf("hook migrate result: %w", err)
	}
	call.Post(abi.Event{Kind: abi.EventHookDone, ID: call.ID, Phase: abi.OutcomeResult, Payload: body})
	return nil
}

// afterRun runs one `afterRun` handler (generation.ts finish): the host checks the tasks it asks for, off the line, as the core
// creates only checked ones: the kind must be an installed task that is not built in, its input JSON, and its definition's `initial()`
// a JSON checkpoint of one of its phases. The answer is {tasks: [{task, version, input, checkpoint}], rejected?: [{name, message}]}.
func (h *Hooks) afterRun(ctx context.Context, call *sqlhost.Call, p hookEffect) error {
	target, ok := h.Lookup(p.Handler)
	if !ok {
		return fmt.Errorf("hook effect %d names handler %d, which the registry does not hold", call.ID, p.Handler)
	}
	hooks, ok := target.(*harness.GenerationHooks)
	if !ok {
		return mismatch(p.Name, target)
	}
	var in struct {
		Run harness.RunSummary `json:"run"`
	}
	if err := json.Unmarshal(p.Payload, &in); err != nil {
		return protocol(p.Name, err)
	}
	post := func(phase uint8, payload json.RawMessage) error {
		call.Post(abi.Event{Kind: abi.EventHookDone, ID: call.ID, Phase: phase, Payload: payload})
		return nil
	}
	if hooks.AfterRun == nil {
		return post(abi.OutcomeResult, json.RawMessage("null"))
	}
	api := h.API(durable.TaskId(p.TaskID), durable.ConversationId(p.ConversationID))
	var asked *harness.HookTasks
	if err := callDefinition(func() (err error) { asked, err = hooks.AfterRun(ctx, in.Run, api); return err }); err != nil {
		return post(abi.OutcomeThrown, wireError(err))
	}
	if asked == nil {
		return post(abi.OutcomeResult, json.RawMessage("null"))
	}
	type preparedWire struct {
		Task       string            `json:"task"`
		Version    int               `json:"version"`
		Input      durable.JsonValue `json:"input"`
		Checkpoint durable.JsonValue `json:"checkpoint"`
	}
	type rejectedWire struct {
		Name    string `json:"name"`
		Message string `json:"message"`
	}
	tasks := []preparedWire{}
	rejected := []rejectedWire{}
	reject := func(err error) { rejected = append(rejected, rejectedWire{"Error", err.Error()}) }
	for _, request := range asked.Tasks {
		if request.Task.Definition == nil {
			reject(errors.New("A hook asked for a task without a definition"))
			continue
		}
		name := request.Task.Definition.Name
		if strings.HasPrefix(name, "pi.") {
			reject(fmt.Errorf("A hook cannot start the built-in task %s", name))
			continue
		}
		var installed durable.AnyTask
		if h.Task != nil {
			installed, _ = h.Task(name)
		}
		if installed.Definition == nil {
			reject(fmt.Errorf("Task %s is not installed", name))
			continue
		}
		input, err := durable.ToJsonValue(request.Input)
		if err != nil {
			reject(err)
			continue
		}
		var checkpoint durable.JsonValue
		if err := callDefinition(func() error {
			initial, err := installed.Definition.Initial(input)
			if err != nil {
				return err
			}
			checkpoint, err = durable.ToJsonValue(initial)
			return err
		}); err != nil {
			reject(err)
			continue
		}
		phase, err := durable.FromJsonValue[struct {
			Phase string `json:"phase"`
		}](checkpoint)
		if err != nil {
			reject(err)
			continue
		}
		if _, ok := installed.Definition.Phases[phase.Phase]; !ok {
			reject(fmt.Errorf("Task %s starts at the unknown phase %s", name, phase.Phase))
			continue
		}
		tasks = append(tasks, preparedWire{name, installed.Definition.Version, input, checkpoint})
	}
	// `rejected` is part of the answer only when a request was rejected (PROTOCOL.md, hook afterRun).
	var body []byte
	var err error
	if len(rejected) == 0 {
		body, err = json.Marshal(struct {
			Tasks []preparedWire `json:"tasks"`
		}{tasks})
	} else {
		body, err = json.Marshal(struct {
			Tasks    []preparedWire `json:"tasks"`
			Rejected []rejectedWire `json:"rejected"`
		}{tasks, rejected})
	}
	if err != nil {
		return fmt.Errorf("hook afterRun result: %w", err)
	}
	return post(abi.OutcomeResult, body)
}

// mismatch is a protocol failure: the core asked a handler for a hook it did not register.
func mismatch(name string, target any) error {
	return &protocolError{fmt.Errorf("hook %s: handler is a %T", name, target)}
}

// protocolError is a failure the core's payload or registration caused, not the hook: it discards the handle instead of
// completing the hook as thrown.
type protocolError struct{ error }

func (e *protocolError) Unwrap() error { return e.error }

func protocol(name string, err error) error {
	return &protocolError{fmt.Errorf("hook %s payload: %w", name, err)}
}

// runHook returns the JSON-marshalable result of one hook: the object the hook's result type takes in Pi, or nil.
func runHook(ctx context.Context, p hookEffect, target any, api harness.HookApi, models func() durable.Models) (any, error) {
	switch p.Name {
	case "beforeProviderRequest":
		hooks, ok := target.(*harness.GenerationHooks)
		if !ok {
			return nil, mismatch(p.Name, target)
		}
		var in struct {
			Model   payload.ModelRef `json:"model"`
			Payload any              `json:"payload"`
		}
		if err := json.Unmarshal(p.Payload, &in); err != nil {
			return nil, protocol(p.Name, err)
		}
		if hooks.BeforeProviderRequest == nil {
			return nil, nil
		}
		out, err := hooks.BeforeProviderRequest(ctx, harness.ProviderRequest{Model: models().GetModel(in.Model.Provider, in.Model.ModelID), Payload: in.Payload}, api)
		if out == nil || err != nil {
			return nil, err
		}
		return struct {
			Payload any `json:"payload"`
		}{out.Payload}, nil
	case "beforeRequest":
		hooks, ok := target.(*harness.GenerationHooks)
		if !ok {
			return nil, mismatch(p.Name, target)
		}
		var in struct {
			Messages json.RawMessage `json:"messages"`
		}
		if err := json.Unmarshal(p.Payload, &in); err != nil {
			return nil, protocol(p.Name, err)
		}
		messages, err := durable.DecodeMessages(in.Messages)
		if err != nil {
			return nil, protocol(p.Name, err)
		}
		if hooks.BeforeRequest == nil {
			return nil, nil
		}
		out, err := hooks.BeforeRequest(ctx, harness.GenerationRequest{Messages: messages}, api)
		if out == nil || err != nil {
			return nil, err
		}
		return struct {
			Messages []ai.Message `json:"messages"`
		}{out.Messages}, nil
	case "afterResponse":
		hooks, ok := target.(*harness.GenerationHooks)
		if !ok {
			return nil, mismatch(p.Name, target)
		}
		var in struct {
			Message ai.AssistantMessage `json:"message"`
		}
		if err := json.Unmarshal(p.Payload, &in); err != nil {
			return nil, protocol(p.Name, err)
		}
		if hooks.AfterResponse == nil {
			return nil, nil
		}
		return nil, hooks.AfterResponse(ctx, in.Message, api)
	case "onYield":
		hooks, ok := target.(*harness.GenerationHooks)
		if !ok {
			return nil, mismatch(p.Name, target)
		}
		var in struct {
			Answer ai.AssistantMessage `json:"answer"`
		}
		if err := json.Unmarshal(p.Payload, &in); err != nil {
			return nil, protocol(p.Name, err)
		}
		if hooks.OnYield == nil {
			return nil, nil
		}
		out, err := hooks.OnYield(ctx, in.Answer, api)
		if out == nil || err != nil {
			return nil, err
		}
		switch result := out.(type) {
		case *harness.YieldContinue:
			return struct {
				Continue durable.UserInput `json:"continue"`
			}{result.Continue}, nil
		case *harness.YieldTasks:
			// The host cannot ask the core to run an onYield task: its row shapes are a pin bump (CONTRACT.md section 8).
			return nil, notInPin("onYield tasks")
		default:
			return nil, fmt.Errorf("hook onYield returned a %T", out)
		}
	case "afterTools":
		hooks, ok := target.(*harness.GenerationHooks)
		if !ok {
			return nil, mismatch(p.Name, target)
		}
		var in struct {
			Assistant durable.EntryId   `json:"assistant"`
			Results   []durable.EntryId `json:"results"`
		}
		if err := json.Unmarshal(p.Payload, &in); err != nil {
			return nil, protocol(p.Name, err)
		}
		if hooks.AfterTools == nil {
			return nil, nil
		}
		asked, err := hooks.AfterTools(ctx, in.Assistant, in.Results, api)
		if err != nil {
			return nil, err
		}
		if asked != nil && len(asked.Tasks) > 0 {
			// The generation's afterTools phase is a pin bump (CONTRACT.md section 8): no task of it can start.
			return nil, notInPin("afterTools tasks")
		}
		return nil, nil
	case "beforeTool":
		hooks, ok := target.(*harness.ToolHooks)
		if !ok {
			return nil, mismatch(p.Name, target)
		}
		var in struct {
			Call   ai.ToolCall     `json:"call"`
			Parent *toolParentWire `json:"parent"`
		}
		if err := json.Unmarshal(p.Payload, &in); err != nil {
			return nil, protocol(p.Name, err)
		}
		if hooks.BeforeTool == nil {
			return nil, nil
		}
		out, err := hooks.BeforeTool(ctx, hookCall(in.Call, in.Parent), api)
		if out == nil || err != nil {
			return nil, err
		}
		if out.Block == nil && out.Sleep != nil {
			// A gate's sleep parks the call at its gates, which the core's tool task cannot do (CONTRACT.md section 8); Block wins over Sleep.
			return nil, notInPin("beforeTool sleep")
		}
		// A pointer keeps an explicit empty arguments object: the hook replaced the arguments with {}.
		var arguments *map[string]any
		if out.Arguments != nil {
			arguments = &out.Arguments
		}
		return struct {
			Arguments *map[string]any `json:"arguments,omitempty"`
			Block     *string         `json:"block,omitempty"`
		}{arguments, out.Block}, nil
	case "afterTool":
		hooks, ok := target.(*harness.ToolHooks)
		if !ok {
			return nil, mismatch(p.Name, target)
		}
		var in struct {
			Call   ai.ToolCall     `json:"call"`
			Parent *toolParentWire `json:"parent"`
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(p.Payload, &in); err != nil {
			return nil, protocol(p.Name, err)
		}
		result, err := decodeToolResult(in.Result)
		if err != nil {
			return nil, protocol(p.Name, err)
		}
		if hooks.AfterTool == nil {
			return nil, nil
		}
		out, err := hooks.AfterTool(ctx, hookCall(in.Call, in.Parent), result, api)
		if out == nil || err != nil {
			return nil, err
		}
		if out.Park != nil {
			return nil, notInPin("an afterTool result with a park")
		}
		body, err := encodeToolResult(*out)
		return json.RawMessage(body), err
	case "beforeCompact":
		hooks, ok := target.(*harness.CompactionHooks)
		if !ok {
			return nil, mismatch(p.Name, target)
		}
		var in struct {
			Compaction struct {
				Reason       durable.CompactionReason `json:"reason"`
				Entries      []durable.EntryRecord    `json:"entries"`
				Messages     json.RawMessage          `json:"messages"`
				FirstKept    durable.EntryId          `json:"firstKept"`
				Instructions *string                  `json:"instructions"`
			} `json:"compaction"`
		}
		if err := json.Unmarshal(p.Payload, &in); err != nil {
			return nil, protocol(p.Name, err)
		}
		messages, err := durable.DecodeMessages(in.Compaction.Messages)
		if err != nil {
			return nil, protocol(p.Name, err)
		}
		if hooks.BeforeCompact == nil {
			return nil, nil
		}
		out, err := hooks.BeforeCompact(ctx, harness.CompactionRequest{
			Reason: in.Compaction.Reason, Entries: in.Compaction.Entries, Messages: messages,
			FirstKept: in.Compaction.FirstKept, Instructions: in.Compaction.Instructions,
		}, api)
		if out == nil || err != nil {
			return nil, err
		}
		return struct {
			Decline bool    `json:"decline,omitempty"`
			Summary *string `json:"summary,omitempty"`
		}{out.Decline, out.Summary}, nil
	case "prepareArguments":
		prepare, ok := target.(PrepareArguments)
		if !ok {
			return nil, mismatch(p.Name, target)
		}
		var in struct {
			Arguments any `json:"arguments"`
		}
		if err := json.Unmarshal(p.Payload, &in); err != nil {
			return nil, protocol(p.Name, err)
		}
		return prepare(in.Arguments)
	}
	return nil, &protocolError{fmt.Errorf("hook %s is not a hook the native driver knows", p.Name)}
}

type toolParentWire struct {
	TaskId durable.TaskId `json:"taskId"`
	CallId string         `json:"callId"`
}

// hookCall is the call a tool hook sees, with its parent when the call is nested.
func hookCall(call ai.ToolCall, parent *toolParentWire) harness.ToolHookCall {
	if parent == nil {
		return harness.NewToolHookCall(call, nil)
	}
	return harness.NewToolHookCall(call, &harness.ToolParent{TaskId: parent.TaskId, CallId: parent.CallId})
}

func decodeToolResult(raw json.RawMessage) (durable.ToolExecutionResult, error) {
	var wire struct {
		Content     []json.RawMessage        `json:"output"`
		IsError     *bool                    `json:"isError"`
		Details     json.RawMessage          `json:"details"`
		Diagnostics []durable.ToolDiagnostic `json:"diagnostics"`
		Usage       *ai.Usage                `json:"usage"`
		Control     *durable.ToolControl     `json:"control"`
		// StructuredOutput is kept raw: a present null is a value.
		StructuredOutput json.RawMessage `json:"structuredOutput"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return durable.ToolExecutionResult{}, err
	}
	result := durable.ToolExecutionResult{IsError: wire.IsError, Diagnostics: wire.Diagnostics, Usage: wire.Usage, Control: wire.Control}
	if wire.Content != nil {
		result.Output = make([]ai.ToolResultMessageContent, 0, len(wire.Content))
		for i, c := range wire.Content {
			block, err := ai.UnmarshalContentBlock(c)
			if err != nil {
				return result, fmt.Errorf("tool result content[%d]: %w", i, err)
			}
			typed, ok := block.(ai.ToolResultMessageContent)
			if !ok {
				return result, fmt.Errorf("tool result content[%d] is a %T", i, block)
			}
			result.Output = append(result.Output, typed)
		}
	}
	if wire.StructuredOutput != nil {
		result.HasStructuredOutput = true
		if err := json.Unmarshal(wire.StructuredOutput, &result.StructuredOutput); err != nil {
			return result, err
		}
	}
	if wire.Details != nil {
		result.HasDetails = true
		if err := json.Unmarshal(wire.Details, &result.Details); err != nil {
			return result, err
		}
	}
	return result, nil
}
