package execution

// Ports packages/agent/src/harness/execution/tools.ts.

import (
	"encoding/json"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/ai"
)

// PreparedToolCall is a resolved tool whose prepared arguments passed validation.
type PreparedToolCall struct {
	ToolCall *ai.ToolCall
	Tool     *harness.AgentHarnessTool
	Args     map[string]any
}

// ImmediateToolOutcome is an error result produced without executing an external effect.
type ImmediateToolOutcome struct {
	Kind      string
	ToolCall  *ai.ToolCall
	Result    harness.AgentToolResult
	IsError   bool
	Terminate bool
}

// ToolBlock refuses a tool invocation with an optional termination hint.
type ToolBlock struct {
	Reason    string
	Terminate bool
}

// BeforeToolDecision replaces arguments or blocks a prepared call.
type BeforeToolDecision struct {
	Args  map[string]any
	Block *ToolBlock
}

// ClearedToolCall is a validated call cleared for durable intent publication and execution.
type ClearedToolCall struct {
	ToolCall *ai.ToolCall
	Tool     *harness.AgentHarnessTool
	Args     map[string]any
}

// ExecutedToolCall is the raw tool output before after-tool patching.
type ExecutedToolCall struct {
	Result  harness.AgentToolResult
	IsError bool
}

// AfterToolPatch changes only present fields. SetDetails distinguishes explicit JSON null from an omitted Details field.
type AfterToolPatch struct {
	Content    []ai.ToolResultMessageContent
	Details    any
	SetDetails bool
	IsError    *bool
	Usage      *ai.Usage
	Terminate  *bool
}

// FinalizedToolCall is ready to become a durable tool-result message.
type FinalizedToolCall struct {
	ToolCall  *ai.ToolCall
	Result    harness.AgentToolResult
	IsError   bool
	Terminate bool
}

func errorToolResult(message string) harness.AgentToolResult {
	return harness.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: message}}}
}

func immediateError(call *ai.ToolCall, message string, terminate bool) *ImmediateToolOutcome {
	return &ImmediateToolOutcome{Kind: "immediate", ToolCall: call, Result: errorToolResult(message), IsError: true, Terminate: terminate}
}

// PrepareToolCall resolves a tool, applies argument preparation, and validates the result. Exactly one returned outcome is non-nil.
func PrepareToolCall(call *ai.ToolCall, tools []harness.AgentHarnessTool) (prepared *PreparedToolCall, immediate *ImmediateToolOutcome) {
	var tool *harness.AgentHarnessTool
	for i := range tools {
		if tools[i].Name == call.Name {
			tool = &tools[i]
			break
		}
	}
	if tool == nil {
		return nil, immediateError(call, "Tool "+ai.SafeJsonStringify(call.Name)+" is unavailable", false)
	}
	defer func() {
		if failure := recover(); failure != nil {
			prepared, immediate = nil, immediateError(call, fmt.Sprint(failure), false)
		}
	}()
	arguments := any(call.Arguments)
	if tool.PrepareArguments != nil {
		var err error
		arguments, err = tool.PrepareArguments(arguments)
		if err != nil {
			return nil, immediateError(call, err.Error(), false)
		}
	}
	encoded, err := json.Marshal(arguments)
	if err != nil {
		return nil, immediateError(call, err.Error(), false)
	}
	preparedCall := *call
	preparedCall.Arguments = nil
	if err := json.Unmarshal(encoded, &preparedCall.Arguments); err != nil {
		return nil, immediateError(call, err.Error(), false)
	}
	args, err := agent.ValidateToolArguments(tool.ToolSchema, preparedCall)
	if err != nil {
		return nil, immediateError(call, err.Error(), false)
	}
	return &PreparedToolCall{ToolCall: call, Tool: tool, Args: args}, nil
}

// ApplyBeforeToolDecision applies a block or revalidates replacement arguments. Exactly one returned outcome is non-nil.
func ApplyBeforeToolDecision(prepared *PreparedToolCall, decision *BeforeToolDecision) (cleared *ClearedToolCall, immediate *ImmediateToolOutcome) {
	if decision != nil && decision.Block != nil {
		return nil, immediateError(prepared.ToolCall, decision.Block.Reason, decision.Block.Terminate)
	}
	args := prepared.Args
	if decision != nil && decision.Args != nil {
		defer func() {
			if failure := recover(); failure != nil {
				cleared, immediate = nil, immediateError(prepared.ToolCall, fmt.Sprint(failure), false)
			}
		}()
		call := *prepared.ToolCall
		call.Arguments = ai.JsonObject(decision.Args)
		var err error
		args, err = agent.ValidateToolArguments(prepared.Tool.ToolSchema, call)
		if err != nil {
			return nil, immediateError(prepared.ToolCall, err.Error(), false)
		}
	}
	return &ClearedToolCall{ToolCall: prepared.ToolCall, Tool: prepared.Tool, Args: args}, nil
}

// ExecuteToolCall admits and waits for a tool effect. Gate and pre-aborted invocation failures escape; tool errors and panics become error output. Updates after settlement are ignored.
func ExecuteToolCall(ctx harness.Context, call *ClearedToolCall, gate *Gate, onUpdate harness.AgentHarnessToolUpdateCallback, toolContext any, invocation harness.AgentHarnessToolInvocation) (ExecutedToolCall, error) {
	var executed ExecutedToolCall
	var accepting atomic.Bool
	accepting.Store(true)
	err := gate.Admit(func() error {
		admitted := harness.WithAbortSignal(ctx, gate.Signal())
		if admitted.Err() != nil {
			return harness.AbortError(admitted)
		}
		defer accepting.Store(false)
		defer func() {
			if failure := recover(); failure != nil {
				executed = ExecutedToolCall{Result: errorToolResult(fmt.Sprint(failure)), IsError: true}
			}
		}()
		result, err := call.Tool.Execute(admitted, call.ToolCall.ID, call.Args, func(partial harness.AgentToolResult, options harness.AgentHarnessToolUpdateOptions) {
			if accepting.Load() {
				onUpdate(partial, options)
			}
		}, toolContext, invocation)
		if err != nil {
			executed = ExecutedToolCall{Result: errorToolResult(err.Error()), IsError: true}
		} else {
			executed = ExecutedToolCall{Result: result}
		}
		return nil
	})
	return executed, err
}

// FinalizeToolCall applies an after-tool patch field by field without changing the raw result.
func FinalizeToolCall(call *ClearedToolCall, executed ExecutedToolCall, patch *AfterToolPatch) FinalizedToolCall {
	result, isError := executed.Result, executed.IsError
	if patch != nil {
		if patch.Content != nil {
			result.Content = patch.Content
		}
		if patch.Details != nil || patch.SetDetails {
			result.Details = patch.Details
		}
		if patch.Usage != nil {
			result.Usage = patch.Usage
		}
		if patch.Terminate != nil {
			result.Terminate = patch.Terminate
		}
		if patch.IsError != nil {
			isError = *patch.IsError
		}
	}
	return FinalizedToolCall{ToolCall: call.ToolCall, Result: result, IsError: isError, Terminate: result.Terminate != nil && *result.Terminate}
}

// ToolResultFromMessage reconstructs canonical staged tool output.
func ToolResultFromMessage(message agent.ToolResultMessage, terminate bool) harness.AgentToolResult {
	result := harness.AgentToolResult{Content: message.Content, Details: message.Details, Usage: message.Usage}
	if terminate {
		result.Terminate = new(true)
	}
	return result
}

// CreateToolResultMessage converts finalized output to the provider-facing transcript shape, normalizing missing content to an empty array.
func CreateToolResultMessage(call FinalizedToolCall) ai.ToolResultMessage {
	content := call.Result.Content
	if content == nil {
		content = []ai.ToolResultMessageContent{}
	}
	return ai.ToolResultMessage{ToolCallID: call.ToolCall.ID, ToolName: call.ToolCall.Name, Content: content, Details: call.Result.Details, Usage: call.Result.Usage, IsError: call.IsError, Timestamp: time.Now().UnixMilli()}
}
