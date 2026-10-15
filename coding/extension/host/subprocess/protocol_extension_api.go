package subprocess

import (
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// Wire shapes for the upstream 0.99.1 extension API additions: registerMcpServer, registerVirtualModel, getSettings, executeTool and prepareLoadout. They follow the conventions of protocol.go (one current wire, no version field). All SDKs speak them.

// ── MCP servers ──────────────────────────────────────────────────────────────

// McpServerDecl is one MCP server an extension registered. Config is the raw `mcpServers` entry: the host validates it with extension.ValidateMcpServerConfig, as upstream's registerMcpServer does.
type McpServerDecl struct {
	Name   string          `json:"name"`
	Config json.RawMessage `json:"config"`
}

// CallRegisterMcpServer (ext→host) registers a server after load. Args is a McpServerDecl. The result is a McpServersResult; an invalid config or a name another extension registered is a call error, which the SDK raises to the extension as upstream's throw.
const CallRegisterMcpServer = "registerMcpServer"

// CallUnregisterMcpServer (ext→host) removes a server the extension registered. Args is McpServerRef; the result is a McpServersResult.
const CallUnregisterMcpServer = "unregisterMcpServer"

// McpServerRef names one server.
type McpServerRef struct {
	Name string `json:"name"`
}

// McpServersResult is the reply of the MCP registration calls: every registered server after the call, in registration order. The SDK replaces its replicated list with it, so a getMcpServers that follows sees the change.
type McpServersResult struct {
	Servers []extension.RegisteredMcpServer `json:"servers"`
}

// CallGetMcpServers (ext→host) reads the runtime registry, as getMcpServers does (loader.ts:475-478). The result is a McpServersResult. A Node factory may call it before its register frame.
const CallGetMcpServers = "getMcpServers"

// CallCheckMcpServer (ext→host) validates a registration without making it: the config as an `mcpServers` entry and the ownership of the name, which upstream's registerMcpServer does when it is called and throws to the factory (loader.ts:456-468). Args is a McpServerDecl; the error is the throw's message. A Node factory may call it before its register frame, which commits what the factory registered.
const CallCheckMcpServer = "mcpServers.check"

// CallLoadingExec (ext→host) runs a child for a Node factory's pi.exec before its register frame, and for a call a factory's callback made after the factory returned and before the runtime had its registered connection, which the runtime sends right after the register frame. Upstream's loader gives the factory an exec that spawns the child directly, in the loader's working directory, with no session bound (loader.ts:411-414), so the host runs core/exec.ts's execCommand in its own working directory. Args and result are those of "exec". Once the runtime has its registered connection, pi.exec calls "exec", which runs in the bound session.
const CallLoadingExec = "exec.loading"

// ── Virtual models ───────────────────────────────────────────────────────────

// VirtualModelDecl declares a virtual model. It carries every VirtualModelDefinition field except route, which the extension keeps and the host calls with RequestVirtualModelRoute.
type VirtualModelDecl struct {
	Provider       string   `json:"provider"`
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	ThinkingLevels []string `json:"thinkingLevels,omitempty"`
	ContextWindow  int      `json:"contextWindow,omitempty"`
	MaxTokens      int      `json:"maxTokens,omitempty"`
	Input          []string `json:"input,omitempty"`
}

// CallRegisterVirtualModel (ext→host) registers a virtual model after load. Args is a VirtualModelDecl.
const CallRegisterVirtualModel = "registerVirtualModel"

// CallUnregisterVirtualModel (ext→host) removes a virtual model. Args is VirtualModelRef.
const CallUnregisterVirtualModel = "unregisterVirtualModel"

// VirtualModelRef names one virtual model.
type VirtualModelRef struct {
	Provider string `json:"provider"`
	ID       string `json:"id"`
}

// RequestVirtualModelRoute (host→ext) routes one request. Args is a VirtualModelRouteArgs; the response is a VirtualModelRouteResult. A cancel for the request cancels the router's signal.
const RequestVirtualModelRoute = "virtual_model_route"

// VirtualModelRouteArgs is the RequestVirtualModelRoute argument.
type VirtualModelRouteArgs struct {
	VirtualModelRef
	Request VirtualModelRouteRequest `json:"request"`
}

// VirtualModelRouteRequest is upstream's ModelRouteRequest as JSON. Models are Pi's model objects (extension.ModelObject), Messages are session messages.
type VirtualModelRouteRequest struct {
	Model         json.RawMessage            `json:"model"`
	ThinkingLevel string                     `json:"thinkingLevel"`
	Reason        string                     `json:"reason"`
	Previous      *VirtualModelRoutePrevious `json:"previous,omitempty"`
	Failed        *VirtualModelRouteFailed   `json:"failed,omitempty"`
	State         json.RawMessage            `json:"state,omitempty"`
	Messages      []json.RawMessage          `json:"messages"`
}

// VirtualModelRoutePrevious is the physical model and thinking level of the latest successful response.
type VirtualModelRoutePrevious struct {
	Model         json.RawMessage `json:"model"`
	ThinkingLevel string          `json:"thinkingLevel,omitempty"`
}

// VirtualModelRouteFailed is the failed request of a retry; Message is its AssistantMessage.
type VirtualModelRouteFailed struct {
	Model         json.RawMessage `json:"model"`
	ThinkingLevel string          `json:"thinkingLevel,omitempty"`
	Message       json.RawMessage `json:"message"`
}

// VirtualModelRouteResult is upstream's ModelRoute as JSON. Model is the Model object the router returned; the host reads its provider and id and resolves the physical model. A State that is absent keeps the current state.
type VirtualModelRouteResult struct {
	Model         map[string]any  `json:"model"`
	ThinkingLevel string          `json:"thinkingLevel"`
	State         json.RawMessage `json:"state,omitempty"`
	// StateUnchanged reports that the router returned the state object it was given. Upstream stores a router's state unless `route.state === request.state` (agent-session.ts:788), and only a runtime that has object identity can say so: Node sends it, and the host answers the session with the request's own state. The other SDKs leave it unset and the host compares the JSON values.
	StateUnchanged bool `json:"stateUnchanged,omitempty"`
}

// ── executeTool ──────────────────────────────────────────────────────────────

// CallExecuteTool (ext→host) runs another tool for the calling tool, as ctx.executeTool does. Args is an ExecuteToolArgs; the result is an ExecuteToolOutcome. It never fails for tool failures: they come back with IsError set. The call's ParentRequestID names the calling tool's request, whose cancellation cancels the nested call by default.
const CallExecuteTool = "executeTool"

// ExecuteToolArgs is the CallExecuteTool argument.
type ExecuteToolArgs struct {
	// CallerID is the id of the calling tool call.
	CallerID string          `json:"callerId"`
	Name     string          `json:"name"`
	Args     json.RawMessage `json:"args"`
	// ExecuteID names this call for RequestExecuteToolUpdate and CallExecuteToolCancel. It is unique within the connection.
	ExecuteID string `json:"executeId"`
	// WantsUpdates reports an onUpdate callback: the host sends RequestExecuteToolUpdate for each partial result.
	WantsUpdates bool `json:"wantsUpdates,omitempty"`
	// OwnSignal reports that the extension passed options.signal: the nested tool runs with that signal instead of the calling tool's.
	OwnSignal bool `json:"ownSignal,omitempty"`
}

// CallExecuteToolCancel (ext→host) cancels the nested call named by ExecuteToolCancel: the extension's own signal for the call aborted.
const CallExecuteToolCancel = "executeTool.cancel"

// ExecuteToolCancel is the CallExecuteToolCancel argument.
type ExecuteToolCancel struct {
	ExecuteID string `json:"executeId"`
}

// RequestExecuteToolUpdate (host→ext) delivers one partial result of a nested call, in order and before the call's result, and the host waits for the answer. Args is an ExecuteToolUpdate. An error response is the callback's throw: the update raises no `tool_execution_update` event, the later updates are still sent, and the call rejects with the first error once the tool returned (nested-tool-calls.ts:219-231, agent-loop.ts:820-849). The request is not cancelled with the nested call: Pi's callback has no cancellation.
const RequestExecuteToolUpdate = "execute_tool_update"

// ExecuteToolUpdate is the RequestExecuteToolUpdate argument. Result is an AgentToolResult in Pi's shape.
type ExecuteToolUpdate struct {
	ExecuteID string          `json:"executeId"`
	Result    json.RawMessage `json:"result"`
}

// ExecuteToolOutcome is upstream's AgentToolCallOutcome as JSON. Result is an AgentToolResult in Pi's shape (content, details, structuredContent, usage, terminate).
type ExecuteToolOutcome struct {
	ToolCall ai.ToolCall     `json:"toolCall"`
	Result   json.RawMessage `json:"result"`
	IsError  bool            `json:"isError"`
	// DurationMs is the milliseconds execute() took, measured with a monotonic clock; absent when the tool did not run. upstream: agent/src/types.ts:454 AgentToolCallOutcome.durationMs
	DurationMs *int64 `json:"durationMs,omitempty"`
}

// CallGetCallableTools (ext→host) lists the tools ctx.executeTool can call at the moment of the call (runner.ts:958-961). The result is a CallableToolsResult.
const CallGetCallableTools = "getCallableTools"

// CallableToolsResult is the reply of CallGetCallableTools.
type CallableToolsResult struct {
	Tools []extension.AgentTool `json:"tools"`
}

// ── prepareArguments ─────────────────────────────────────────────────────────

// RequestPrepareArguments (host→ext) runs a tool's prepareArguments. RequestPayload.Tool names the tool and Args is the model's arguments; the response is the prepared arguments.
const RequestPrepareArguments = "tool_prepare_arguments"

// ── reachesTools ─────────────────────────────────────────────────────────────

// RequestReachesTools (host→ext) runs a tool's reachesTools. RequestPayload.Tool names the tool and Args is the call's input; the response is the string "all", or an array of the words of the input that may name the tools the call reaches (upstream's `"all" | readonly string[]`).
const RequestReachesTools = "tool_reaches_tools"

// CallToolReachesTools (ext→host) runs the reachesTools of a tool getAllTools lists (upstream ToolInfo.reachesTools, which an extension's tool_call handler calls with the call's input). getAllTools marks such a tool `"reachesTools": true`, since a function cannot cross the wire, and an SDK's ToolInfo turns the mark into a function that makes this call. Args is ToolReachesToolsArgs; the result is the string "all", or the array of words. A tool getAllTools no longer lists with the hook fails the call, and so does the hook's throw.
const CallToolReachesTools = "tool.reachesTools"

// ToolReachesToolsArgs are the arguments of CallToolReachesTools: the tool's name and the input to ask about. An absent input is an empty object.
type ToolReachesToolsArgs struct {
	Tool  string          `json:"tool"`
	Input json.RawMessage `json:"input,omitempty"`
}

// ── prepareLoadout ───────────────────────────────────────────────────────────

// RequestPrepareLoadout (host→ext) runs a tool's prepareLoadout. RequestPayload.Tool names the tool and Args is a ToolLoadoutPayload; the response is an extension.ToolLoadoutChanges, or null for no changes.
const RequestPrepareLoadout = "tool_prepare_loadout"

// ToolLoadoutPayload is upstream's ToolLoadout as JSON. Exposures, Namespaces and PromptGuidelines answer getExposure, getNamespace and getPromptGuidelines for every registered tool.
type ToolLoadoutPayload struct {
	Declared   []extension.AgentTool               `json:"declared"`
	Callable   []extension.AgentTool               `json:"callable"`
	Registered []extension.AgentTool               `json:"registered"`
	Exposures  map[string]extension.ToolExposure   `json:"exposures"`
	Namespaces map[string]*extension.ToolNamespace `json:"namespaces,omitempty"`
	// PromptGuidelines lists the guidelines of each registered tool that has any.
	PromptGuidelines map[string][]string `json:"promptGuidelines,omitempty"`
}

// ── Pure helpers ─────────────────────────────────────────────────────────────

// CallParseFrontmatter (ext→host) is Pi's parseFrontmatter (utils/frontmatter.ts): the YAML header and trimmed body of a markdown document, parsed by the same
// parser the host uses for skills and prompt templates. The args are a ParseFrontmatterArgs and the result a ParseFrontmatterResult. It reads no session or
// extension state, so it may arrive before the register frame, from a factory that dialled lazily.
const CallParseFrontmatter = "frontmatter.parse"

// CallParseDate (ext→host) is `Date.parse(text)` (V8's parser, legacy formats included), the time value `new Date(text).getTime()` reads. The args are a
// ParseDateArgs and the result a ParseDateResult. It may arrive before the register frame.
const CallParseDate = "date.parse"

// ParseFrontmatterArgs is the argument of CallParseFrontmatter.
type ParseFrontmatterArgs struct {
	Content string `json:"content"`
}

// ParseFrontmatterResult is the result of CallParseFrontmatter. Frontmatter is the JSON of `parse(yaml) ?? {}`: an object lists its keys in the order a
// JavaScript object enumerates them (array-index keys first, ascending, then insertion order), and any other YAML document is its scalar or array.
type ParseFrontmatterResult struct {
	Frontmatter json.RawMessage `json:"frontmatter"`
	Body        string          `json:"body"`
}

// ParseDateArgs is the argument of CallParseDate.
type ParseDateArgs struct {
	Text string `json:"text"`
}

// ParseDateResult is the result of CallParseDate: the time value in milliseconds, null when Date.parse reads NaN.
type ParseDateResult struct {
	Ms *float64 `json:"ms"`
}
