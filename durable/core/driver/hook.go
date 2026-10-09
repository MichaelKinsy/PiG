// SPDX-License-Identifier: MIT

package driver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/abi"
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
}

// Handler returns the sqlhost handler of hook effects.
func (h *Hooks) Handler() sqlhost.Handler { return h.run }

func (h *Hooks) run(ctx context.Context, call *sqlhost.Call) error {
	var p hookEffect
	if err := json.Unmarshal(call.Payload, &p); err != nil {
		return fmt.Errorf("hook payload: %w", err)
	}
	target, ok := h.Lookup(p.Handler)
	if !ok {
		return fmt.Errorf("hook effect %d names handler %d, which the registry does not hold", call.ID, p.Handler)
	}
	api := h.API(durable.TaskId(p.TaskID), durable.ConversationId(p.ConversationID))
	value, err := runHook(ctx, p, target, api)
	if ctx.Err() != nil {
		return nil
	}
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
func runHook(ctx context.Context, p hookEffect, target any, api harness.HookApi) (any, error) {
	switch p.Name {
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
		return struct {
			Continue durable.UserInput `json:"continue"`
		}{out.Continue}, nil
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
		return nil, hooks.AfterTools(ctx, in.Assistant, in.Results, api)
	case "beforeTool":
		hooks, ok := target.(*harness.ToolHooks)
		if !ok {
			return nil, mismatch(p.Name, target)
		}
		var in struct {
			Call ai.ToolCall `json:"call"`
		}
		if err := json.Unmarshal(p.Payload, &in); err != nil {
			return nil, protocol(p.Name, err)
		}
		if hooks.BeforeTool == nil {
			return nil, nil
		}
		out, err := hooks.BeforeTool(ctx, in.Call, api)
		if out == nil || err != nil {
			return nil, err
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
		out, err := hooks.AfterTool(ctx, in.Call, result, api)
		if out == nil || err != nil {
			return nil, err
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

func decodeToolResult(raw json.RawMessage) (durable.ToolExecutionResult, error) {
	var wire struct {
		Content     []json.RawMessage        `json:"content"`
		IsError     *bool                    `json:"isError"`
		Details     json.RawMessage          `json:"details"`
		Diagnostics []durable.ToolDiagnostic `json:"diagnostics"`
		Usage       *ai.Usage                `json:"usage"`
		Control     *durable.ToolControl     `json:"control"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return durable.ToolExecutionResult{}, err
	}
	result := durable.ToolExecutionResult{IsError: wire.IsError, Diagnostics: wire.Diagnostics, Usage: wire.Usage, Control: wire.Control}
	if wire.Content != nil {
		result.Content = make([]ai.ToolResultMessageContent, 0, len(wire.Content))
		for i, c := range wire.Content {
			block, err := ai.UnmarshalContentBlock(c)
			if err != nil {
				return result, fmt.Errorf("tool result content[%d]: %w", i, err)
			}
			typed, ok := block.(ai.ToolResultMessageContent)
			if !ok {
				return result, fmt.Errorf("tool result content[%d] is a %T", i, block)
			}
			result.Content = append(result.Content, typed)
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
