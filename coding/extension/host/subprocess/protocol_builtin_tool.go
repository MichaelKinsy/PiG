package subprocess

import (
	"encoding/json"

	"github.com/MichaelKinsy/PiG/ai"
)

// ── Built-in tool delegation ─────────────────────────────────────────────────
//
// Pi's `create<Tool>ToolDefinition(cwd, options)` factories (core/tools/{read,write,edit,bash,grep,find,ls,powershell}.ts, exported from index.ts) return the
// built-in tool's ToolDefinition, which an extension spreads into pi.registerTool to override the built-in and whose `execute` it calls to delegate. The native
// SDKs reach the same implementation through the host: CallBuiltinToolDefinition is the definition's metadata, CallBuiltinToolExecute runs `execute`, and
// CallBuiltinToolLocalExec is `createLocalBashOperations().exec` / `createLocalPowerShellOperations().exec`. Pi's own callbacks (`operations`, `spawnHook`)
// stay in the extension and are called back with RequestBuiltinToolCallback while the call runs.

// CallBuiltinToolDefinition (ext→host) is the ToolDefinition that create<Tool>ToolDefinition(cwd, options) returns, without its `execute` and renderers. Args
// is a BuiltinToolDefinitionArgs and the result a BuiltinToolDefinitionResult. It reads no session state, so it may arrive before the register frame, from a
// factory that dialled lazily.
const CallBuiltinToolDefinition = "builtinTool.definition"

// CallBuiltinToolExecute (ext→host) runs `execute(toolCallId, params, signal, onUpdate, ctx)` of the definition create<Tool>ToolDefinition(cwd, options)
// returns. Args is a BuiltinToolExecuteArgs; the result is the AgentToolResult the tool resolved to, in Pi's shape. A tool that throws is an error reply
// carrying the thrown message, and a tool that returns `isError: true` is a result, as in Pi.
//
// The call's ParentRequestID is the calling tool's request when the extension forwards its own signal (the request's cancellation is then the tool's signal),
// and empty when the extension passes another signal or none: the call is then detached and only CallExecuteToolCancel, naming ExecuteID, aborts it.
const CallBuiltinToolExecute = "builtinTool.execute"

// CallBuiltinToolLocalExec (ext→host) is `exec` of the BashOperations createLocalBashOperations(options) or createLocalPowerShellOperations() returns: the
// command runs through the host's shell tool implementation, its output reaches the extension in order as RequestBuiltinToolExecData requests before the
// reply, and the reply is Pi's `{ exitCode }`. Args is a BuiltinToolLocalExecArgs. A command Pi's exec rejects is an error reply with Pi's message
// ("aborted", "timeout:<seconds>", a missing working directory). CallExecuteToolCancel, naming ExecuteID, is the exec's AbortSignal.
const CallBuiltinToolLocalExec = "builtinTool.localExec"

// RequestBuiltinToolCallback (host→ext) calls one of the callbacks of a running CallBuiltinToolExecute: Args is a BuiltinToolCallback and the response is the
// callback's result (JSON null for a callback that returns nothing). An error response is the callback's throw. The request is not cancelled with the
// tool's signal: the callback's own `signal` is the tool's.
const RequestBuiltinToolCallback = "builtin_tool_callback"

// RequestBuiltinToolExecData (host→ext) delivers one chunk of the output of a CallBuiltinToolLocalExec, in order and before the call's reply. Args is a
// BuiltinToolExecData.
const RequestBuiltinToolExecData = "builtin_tool_exec_data"

// BuiltinToolOptions are the options of Pi's create<Tool>ToolDefinition that cross the wire. `operations` and `spawnHook` hold functions, which stay in the
// extension: Operations names the members the extension supplied and SpawnHook reports a hook, and BuiltinToolExecuteArgs.Callbacks names the extension's
// table of them. A member Operations does not name keeps the tool's local implementation, as an undefined member of a partial Pi object would not.
type BuiltinToolOptions struct {
	// AutoResizeImages is ReadToolOptions.autoResizeImages.
	AutoResizeImages *bool `json:"autoResizeImages,omitempty"`
	// ResizeOptions is ReadToolOptions.resizeOptions.
	ResizeOptions *ai.ModelImageResizeOptions `json:"resizeOptions,omitempty"`
	// CommandPrefix is BashToolOptions.commandPrefix.
	CommandPrefix string `json:"commandPrefix,omitempty"`
	// ShellPath is BashToolOptions.shellPath.
	ShellPath string `json:"shellPath,omitempty"`
	// ExposeSessionEnvironment is BashToolOptions.exposeSessionEnvironment and PowerShellToolOptions.exposeSessionEnvironment.
	ExposeSessionEnvironment *bool `json:"exposeSessionEnvironment,omitempty"`
	// Operations names the members of the tool's `operations` object the extension supplied.
	// An empty list is an operations object with no member, which the host tells from an absent one (newBuiltinTool).
	Operations []string `json:"operations"`
	// SpawnHook reports the extension supplied `spawnHook`.
	SpawnHook bool `json:"spawnHook,omitempty"`
}

// BuiltinToolDefinitionArgs are the arguments of CallBuiltinToolDefinition. Name is a built-in tool name: read, bash, powershell, edit, write, grep, find or ls.
type BuiltinToolDefinitionArgs struct {
	Name    string              `json:"name"`
	Cwd     string              `json:"cwd"`
	Options *BuiltinToolOptions `json:"options,omitempty"`
}

// BuiltinToolDefinitionResult is the reply of CallBuiltinToolDefinition: the members of Pi's ToolDefinition for a built-in tool that are data. Absent
// members are Pi's undefined.
type BuiltinToolDefinitionResult struct {
	Name          string `json:"name"`
	Label         string `json:"label"`
	Description   string `json:"description"`
	PromptSnippet string `json:"promptSnippet,omitempty"`
	//portlint:allow emptydrop Pi's definition.promptGuidelines is undefined or a non-empty list for the built-in tools, and the SDKs read an absent list as undefined
	PromptGuidelines    []string        `json:"promptGuidelines,omitempty"`
	Parameters          json.RawMessage `json:"parameters"`
	OutputSchema        json.RawMessage `json:"outputSchema,omitempty"`
	ConstrainedSampling json.RawMessage `json:"constrainedSampling,omitempty"`
	ExecutionMode       string          `json:"executionMode,omitempty"`
	// RenderShell is "self" for a tool that draws its own framing (edit).
	RenderShell string `json:"renderShell,omitempty"`
}

// BuiltinToolExecuteArgs are the arguments of CallBuiltinToolExecute.
type BuiltinToolExecuteArgs struct {
	Name    string              `json:"name"`
	Cwd     string              `json:"cwd"`
	Options *BuiltinToolOptions `json:"options,omitempty"`
	// ToolCallID is the `toolCallId` argument of execute.
	ToolCallID string          `json:"toolCallId"`
	Params     json.RawMessage `json:"params"`
	// ExecuteID names this call for RequestExecuteToolUpdate and CallExecuteToolCancel. It is unique within the connection.
	ExecuteID string `json:"executeId"`
	// WantsUpdates reports an onUpdate callback: the host sends RequestExecuteToolUpdate for each partial result.
	WantsUpdates bool `json:"wantsUpdates,omitempty"`
	// Context reports that the extension passed its ExtensionContext as execute's fifth argument: the tool then reads the session's working directory,
	// model and session identity from it, as `ctx?.cwd || cwd` and `ctx?.model` do. Without it the tool uses Cwd and has no session.
	Context bool `json:"context,omitempty"`
	// Callbacks names the extension's table of the callbacks Options announces, for RequestBuiltinToolCallback.
	Callbacks string `json:"callbacks,omitempty"`
	// Aborted reports that the signal the extension passed was aborted before the call: Pi's tool checks `signal.aborted` before it starts, so a cancel
	// sent after the call could lose the race with a tool that finishes at once.
	Aborted bool `json:"aborted,omitempty"`
}

// BuiltinToolCallback is the argument of RequestBuiltinToolCallback. Member is "spawnHook" or "operations.<member>" and Args the member's arguments as a JSON
// object: file paths and text as strings, binary content as base64 strings (the member's own table in the SDKs).
type BuiltinToolCallback struct {
	Handle string          `json:"handle"`
	Member string          `json:"member"`
	Args   json.RawMessage `json:"args,omitempty"`
}

// BuiltinToolLocalExecArgs are the arguments of CallBuiltinToolLocalExec. Shell is "bash" or "powershell". ShellPath is createLocalBashOperations'
// `shellPath`. Timeout is seconds; Env, when present even if empty, replaces the inherited environment.
type BuiltinToolLocalExecArgs struct {
	Shell     string             `json:"shell"`
	ShellPath string             `json:"shellPath,omitempty"`
	Command   string             `json:"command"`
	Cwd       string             `json:"cwd"`
	Timeout   *float64           `json:"timeout,omitempty"`
	Env       *map[string]string `json:"env,omitempty"`
	ExecuteID string             `json:"executeId"`
	// Aborted reports that the extension's signal was aborted before the call, as for BuiltinToolExecuteArgs.
	Aborted bool `json:"aborted,omitempty"`
}

// BuiltinToolExecData is the argument of RequestBuiltinToolExecData: Data is the chunk, base64.
type BuiltinToolExecData struct {
	ExecuteID string `json:"executeId"`
	Data      string `json:"data"`
}

// BuiltinToolLocalExecResult is the reply of CallBuiltinToolLocalExec: Pi's `{ exitCode }`, null for a command that ended without an exit code.
type BuiltinToolLocalExecResult struct {
	ExitCode *int `json:"exitCode"`
}
