package sdk

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// Ports packages/coding-agent/src/core/extensions/types.ts (ToolExposure, ToolAnnotations, ToolNamespace, ToolLoadout, ToolLoadoutChanges, ExecuteToolOptions, ExtensionToolContext).
// Ports packages/coding-agent/src/core/extensions/runner.ts (createToolContext).

// ToolExposure is how the model reaches a tool. "Callable" means callable from other tools through [Context.ExecuteTool], as the codemode tool does.
//
//   - direct: declared to the model while active, and callable while active.
//   - model-only: declared to the model while active, never callable.
//   - codemode: callable whenever registered. Not declared to the model unless explicitly activated.
//   - deferred: like codemode, but codemode tools do not list it; tool search can find it.
//   - hidden: registered but unreachable. Activating it has no effect.
//
// `direct` and `model-only` tools are activated when they are registered; the others are not.
//
// upstream: types.ts:509 (ToolExposure)
type ToolExposure string

// The exposures of upstream's ToolExposure union.
const (
	ToolExposureDirect    ToolExposure = "direct"
	ToolExposureModelOnly ToolExposure = "model-only"
	ToolExposureCodemode  ToolExposure = "codemode"
	ToolExposureDeferred  ToolExposure = "deferred"
	ToolExposureHidden    ToolExposure = "hidden"
)

// ToolAnnotations are hints about what a tool does, with the meaning of MCP tool annotations. They come from the tool's author and are not verified.
//
// upstream: types.ts:515 (ToolAnnotations)
type ToolAnnotations struct {
	ReadOnlyHint    *bool `json:"readOnlyHint,omitempty"`
	DestructiveHint *bool `json:"destructiveHint,omitempty"`
	IdempotentHint  *bool `json:"idempotentHint,omitempty"`
	OpenWorldHint   *bool `json:"openWorldHint,omitempty"`
}

// ToolNamespace is a group of related tools, such as the tools of one MCP server. It is comparable: == compares every field, the aliases
// as a list of names in order.
//
// upstream: types.ts:527 (ToolNamespace)
type ToolNamespace struct {
	Name string `json:"name"`
	// Aliases are other names that refer to the namespace where tools look it up by name (codemode's describeNamespace() and the namespace option of searchTools()), for example `docs` for `mcp__docs`.
	// No aliases leave the member out, as an empty list did.
	Aliases ToolNamespaceAliases `json:"aliases,omitzero"`
	// Description is a short summary shown once with the group in model-facing tool listings.
	Description string `json:"description,omitempty"`
	// Instructions is longer usage guidance, such as MCP server instructions. It is not part of tool listings; tools
	// that describe the namespace on request (codemode's describeNamespace()) return it.
	Instructions string `json:"instructions,omitempty"`
}

// ToolNamespaceAliases is the aliases of a [ToolNamespace], made with [NewToolNamespaceAliases] and read with [ToolNamespaceAliases.Names].
// It encodes as upstream's `aliases` array of strings. The zero value is no aliases.
//
// pig Go mechanic (not a divergence): upstream's `readonly string[]` is held in a string (each name after its length as a uvarint), so
// ToolNamespace stays comparable as it was before aliases existed.
//
// upstream: types.ts:537-541 (ToolNamespace.aliases)
type ToolNamespaceAliases struct {
	packed string
}

// NewToolNamespaceAliases returns the aliases names, in order.
func NewToolNamespaceAliases(names ...string) ToolNamespaceAliases {
	size := 0
	for _, name := range names {
		size += uvarintLen(uint64(len(name))) + len(name)
	}
	packed := make([]byte, 0, size)
	for _, name := range names {
		packed = binary.AppendUvarint(packed, uint64(len(name)))
		packed = append(packed, name...)
	}
	return ToolNamespaceAliases{packed: string(packed)}
}

// Names returns the aliases in order, or nil for none.
func (a ToolNamespaceAliases) Names() []string {
	if a.packed == "" {
		return nil
	}
	count := 0
	for rest := a.packed; rest != ""; count++ {
		size, width := packedUvarint(rest)
		rest = rest[width+size:]
	}
	names := make([]string, 0, count)
	for rest := a.packed; rest != ""; {
		size, width := packedUvarint(rest)
		names = append(names, rest[width:width+size])
		rest = rest[width+size:]
	}
	return names
}

// MarshalJSON encodes the aliases as a JSON array of strings (null for none, as a nil slice).
func (a ToolNamespaceAliases) MarshalJSON() ([]byte, error) {
	return json.Marshal(a.Names())
}

// UnmarshalJSON decodes a JSON array of strings; null is no aliases.
func (a *ToolNamespaceAliases) UnmarshalJSON(data []byte) error {
	var names []string
	if err := json.Unmarshal(data, &names); err != nil {
		return err
	}
	*a = NewToolNamespaceAliases(names...)
	return nil
}

// uvarintLen is the length of x as binary.AppendUvarint writes it.
func uvarintLen(x uint64) int {
	n := 1
	for ; x >= 0x80; x >>= 7 {
		n++
	}
	return n
}

// packedUvarint reads the uvarint length at the start of a packed alias list: the length and the bytes it takes. The list is only ever
// built by NewToolNamespaceAliases, so it is well formed.
func packedUvarint(s string) (size, width int) {
	var x uint64
	var shift uint
	for i := 0; ; i++ {
		b := s[i]
		x |= uint64(b&0x7f) << shift
		if b < 0x80 {
			return int(x), i + 1
		}
		shift += 7
	}
}

// AgentTool is the read-only view of a tool that a tool call sees through [Context.Tools] and [ToolLoadout]. It holds the declaration fields; a tool runs only through [Context.ExecuteTool].
//
// upstream: packages/agent/src/types.ts (AgentTool)
type AgentTool struct {
	Name                string          `json:"name"`
	Label               string          `json:"label"`
	Description         string          `json:"description"`
	Parameters          json.RawMessage `json:"parameters"`
	OutputSchema        json.RawMessage `json:"outputSchema,omitempty"`
	ConstrainedSampling json.RawMessage `json:"constrainedSampling,omitempty"`
	ExecutionMode       string          `json:"executionMode,omitempty"`
}

// ToolLoadout is the tools of a session as [ToolDefinition.PrepareLoadout] sees them.
//
// upstream: types.ts:541-551 (ToolLoadout)
type ToolLoadout struct {
	// Declared are the tools declared to the model (the active tools), in order, with their original descriptions.
	Declared []AgentTool
	// Callable are the tools callable through [Context.ExecuteTool].
	Callable []AgentTool
	// Registered is every registered tool.
	Registered   []AgentTool
	GetExposure  func(name string) ToolExposure
	GetNamespace func(name string) *ToolNamespace
	// GetPromptGuidelines is a tool's `promptGuidelines`, normalized as the system prompt has them; nil for a tool without any.
	// Hidden declarations leave them out of the system prompt.
	GetPromptGuidelines func(name string) []string
}

// ToolLoadoutChanges are the changes [ToolDefinition.PrepareLoadout] makes to what the model sees.
//
// upstream: types.ts:554-563 (ToolLoadoutChanges)
type ToolLoadoutChanges struct {
	// Descriptions are the model-facing descriptions of declared tools, by tool name.
	Descriptions map[string]string `json:"descriptions,omitempty"`
	// HiddenDeclarations names declared tools whose declarations requests leave out. They stay active and callable.
	HiddenDeclarations []string `json:"hiddenDeclarations,omitempty"`
}

// ToolPrepareLoadoutFunc adjusts how the loadout is presented to the model while its tool is active. A nil result leaves the loadout as it is (upstream returns undefined).
type ToolPrepareLoadoutFunc func(loadout ToolLoadout) *ToolLoadoutChanges

// ToolCall is the tool call block of a nested call.
type ToolCall struct {
	Type      string         `json:"type"`
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// ToolResultContent is one text or image block of a tool result.
type ToolResultContent struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

// AgentToolResult is the result of a tool call in upstream's shape.
//
// upstream: packages/agent/src/types.ts:424-446 (AgentToolResult)
type AgentToolResult struct {
	Content           []ToolResultContent `json:"content"`
	Details           any                 `json:"details,omitempty"`
	StructuredContent any                 `json:"structuredContent,omitempty"`
	Usage             map[string]any      `json:"usage,omitempty"`
	IsError           bool                `json:"isError,omitempty"`
	Terminate         bool                `json:"terminate,omitempty"`
}

// UnmarshalJSON reads a result in Pi's shape. Details and StructuredContent keep the member order of their objects, as JavaScript objects do: every object in
// them is an *OrderedObject (a Go map has no order), which marshals back in the order it was read, so a result a tool returned reaches the host with its
// members where the tool wrote them.
func (r *AgentToolResult) UnmarshalJSON(data []byte) error {
	var raw struct {
		Content           []ToolResultContent `json:"content"`
		Details           json.RawMessage     `json:"details"`
		StructuredContent json.RawMessage     `json:"structuredContent"`
		Usage             map[string]any      `json:"usage"`
		IsError           bool                `json:"isError"`
		Terminate         bool                `json:"terminate"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*r = AgentToolResult{Content: raw.Content, Usage: raw.Usage, IsError: raw.IsError, Terminate: raw.Terminate}
	var err error
	if r.Details, err = decodeOrderedRaw(raw.Details); err != nil {
		return err
	}
	r.StructuredContent, err = decodeOrderedRaw(raw.StructuredContent)
	return err
}

// decodeOrderedRaw reads an optional member as an ordered value: nil for an absent member or JSON null.
func decodeOrderedRaw(raw json.RawMessage) (any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	return decodeOrderedValue(decoder)
}

// MarshalJSON writes the result as a tool hands it to the host: `content` as the block array (a text block always carries its text, empty or not),
// `structured_content` and `is_error` under the wire's names, and Pi's own members only when set. A result a built-in tool delegation returned
// ([BuiltinToolDefinition]) can therefore be returned from a [ToolFunc], or passed to [Context.OnUpdate], as it is.
func (r AgentToolResult) MarshalJSON() ([]byte, error) {
	type block struct {
		Type     string  `json:"type"`
		Text     *string `json:"text,omitempty"`
		Data     string  `json:"data,omitempty"`
		MimeType string  `json:"mimeType,omitempty"`
	}
	blocks := make([]block, 0, len(r.Content))
	for _, content := range r.Content {
		entry := block{Type: content.Type, Data: content.Data, MimeType: content.MimeType}
		if content.Type != "image" {
			text := content.Text
			entry.Text = &text
		}
		blocks = append(blocks, entry)
	}
	return json.MarshalJS(struct {
		Content           []block        `json:"content"`
		Details           any            `json:"details,omitempty"`
		StructuredContent any            `json:"structured_content,omitempty"`
		Usage             map[string]any `json:"usage,omitempty"`
		IsError           bool           `json:"is_error,omitempty"`
		Terminate         bool           `json:"terminate,omitempty"`
	}{blocks, r.Details, r.StructuredContent, r.Usage, r.IsError, r.Terminate})
}

// Text joins the text blocks of the result.
func (r AgentToolResult) Text() string {
	var text []string
	for _, block := range r.Content {
		if block.Type == "text" {
			text = append(text, block.Text)
		}
	}
	return strings.Join(text, "\n")
}

// AgentToolCallOutcome is the final outcome of a nested tool call after the hooks ran.
//
// upstream: packages/agent/src/types.ts:449-453 (AgentToolCallOutcome)
type AgentToolCallOutcome struct {
	ToolCall ToolCall        `json:"toolCall"`
	Result   AgentToolResult `json:"result"`
	IsError  bool            `json:"isError"`
	// DurationMs is the milliseconds execute() took, measured with a monotonic clock; absent when the tool did not run. upstream: agent/src/types.ts:454 AgentToolCallOutcome.durationMs
	DurationMs *int64 `json:"durationMs,omitempty"`
}

// ExecuteToolOptions are the options of [Context.ExecuteTool].
//
// upstream: types.ts:367-372 (ExecuteToolOptions)
type ExecuteToolOptions struct {
	// Signal cancels the nested call. Default: the calling tool's request. A Signal replaces the request's cancellation: cancelling the calling request then leaves the nested call running and ExecuteTool waits for its outcome.
	Signal context.Context
	// OnUpdate receives the partial results of the nested tool, in order and before the call returns, in addition to `tool_execution_update` events. It runs on its own goroutine, never on the extension's message loop. A panic in it is the callback's throw: later partial results still reach it, and ExecuteTool returns an error with the panic value's message after the tool returned, as Pi's call rejects with the callback's first error.
	OnUpdate func(partial AgentToolResult)
}

// requestExecuteToolUpdate is the host's request carrying one partial result of a nested call. The host waits for the answer, and an error answer is the callback's throw.
const requestExecuteToolUpdate = "execute_tool_update"

// Tools returns the tools [Context.ExecuteTool] can call, as the host lists them when the call is made: a read after [Context.SetActiveTools] in the same handler sees the change. It is valid only inside a tool handler.
//
// upstream: types.ts:385 (ExtensionToolContext.tools), runner.ts:955-965
func (c Context) Tools() ([]AgentTool, error) {
	if c.toolCallID == "" {
		return nil, errors.New("Tools is only available while a tool runs")
	}
	tools, err := hostRequired[[]AgentTool](c, "getCallableTools", nil, "tools")
	if tools == nil && err == nil {
		tools = []AgentTool{}
	}
	return tools, err
}

// ExecuteTool runs another tool through the same validation, hooks and permission checks as a model-issued call. The call gets the id `<calling id>/<n>`, and the `tool_call`, `tool_result` and `tool_execution_*` events carry `parentToolCallId`. It does not appear in the transcript. Nil args are JSON null, as upstream's undefined fails an object schema.
//
// It never fails for tool failures: unknown tools, validation errors, blocked calls and thrown errors come back as an outcome with IsError set. The error is non-nil for a call outside a tool handler, unencodable arguments, or a host or transport failure. It is valid only inside a tool handler.
//
// upstream: types.ts:386-394 (executeTool), runner.ts:966-983
func (c Context) ExecuteTool(name string, args any, options *ExecuteToolOptions) (AgentToolCallOutcome, error) {
	if c.toolCallID == "" {
		return AgentToolCallOutcome{}, errors.New("ExecuteTool is only available while a tool runs")
	}
	rawArgs, err := json.MarshalJS(args)
	if err != nil {
		return AgentToolCallOutcome{}, fmt.Errorf("encode arguments of %s: %w", name, err)
	}
	var signal context.Context
	var onUpdate func(AgentToolResult)
	if options != nil {
		signal, onUpdate = options.Signal, options.OnUpdate
	}
	ext := c.ext
	executeID := fmt.Sprintf("execute-%d", ext.executeSeq.Add(1))
	if onUpdate != nil {
		ext.executeMu.Lock()
		ext.executeUpdates[executeID] = onUpdate
		ext.executeMu.Unlock()
		defer func() {
			ext.executeMu.Lock()
			delete(ext.executeUpdates, executeID)
			ext.executeMu.Unlock()
		}()
	}
	call := struct {
		CallerID     string          `json:"callerId"`
		Name         string          `json:"name"`
		Args         json.RawMessage `json:"args"`
		ExecuteID    string          `json:"executeId"`
		WantsUpdates bool            `json:"wantsUpdates,omitempty"`
		OwnSignal    bool            `json:"ownSignal,omitempty"`
	}{c.toolCallID, name, rawArgs, executeID, onUpdate != nil, signal != nil}

	// The extension's own signal cancels the call through a second host call, ordered after the call's frame. It replaces the calling request's cancellation (upstream: runner.ts:980, `options.signal ?? signal`), so the call is then detached from the request.
	stop := make(chan struct{})
	var watcher sync.WaitGroup
	result, callErr := c.callHostAfterBegin("executeTool", call, signal != nil, func() {
		if signal == nil {
			return
		}
		watcher.Go(func() {
			select {
			case <-signal.Done():
				cancelResult, cancelErr := c.ext.conn.call("executeTool.cancel", struct {
					ExecuteID string `json:"executeId"`
				}{executeID})
				if err := callResultError(cancelResult, cancelErr); err != nil {
					reportHostCallFailure("executeTool.cancel", err, nil)
				}
			case <-stop:
			}
		})
	})
	close(stop)
	watcher.Wait()
	if err := callResultError(result, callErr); err != nil {
		return AgentToolCallOutcome{}, err
	}
	if result == nil {
		return AgentToolCallOutcome{}, errors.New("host returned no result for executeTool")
	}
	var outcome AgentToolCallOutcome
	if err := json.Unmarshal(result.Result, &outcome); err != nil {
		return AgentToolCallOutcome{}, fmt.Errorf("host reply to executeTool: %w", err)
	}
	return outcome, nil
}

// dispatchExecuteToolUpdate runs the OnUpdate callback of the nested call for one partial result the host sent, and returns what the host waits for: nil, or the callback's throw. Pi calls onUpdate for every partial result, and a throw rejects the call with the first error after the tool returned (nested-tool-calls.ts:219-231, agent-loop.ts:833-845). A panic is the throw; its value is the error's message. An update for a call that ended is dropped.
func (e *Extension) dispatchExecuteToolUpdate(args json.RawMessage) (failure error) {
	var update struct {
		ExecuteID string          `json:"executeId"`
		Result    AgentToolResult `json:"result"`
	}
	if err := json.Unmarshal(args, &update); err != nil {
		return fmt.Errorf("decode %s: %w", requestExecuteToolUpdate, err)
	}
	e.executeMu.Lock()
	onUpdate := e.executeUpdates[update.ExecuteID]
	e.executeMu.Unlock()
	if onUpdate == nil {
		return nil
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			if err, ok := recovered.(error); ok {
				failure = err
			} else {
				failure = fmt.Errorf("%v", recovered)
			}
		}
	}()
	onUpdate(update.Result)
	return nil
}

// ToolReach is the result of a tool's ReachesTools: every tool, or the words of the input that may name them, where a word refers to a tool or namespace whose name contains it, so words may over-match.
//
// pig Go mechanic (not a divergence): a struct stands for upstream's `"all" | readonly string[]`.
//
// upstream: types.ts:621 (reachesTools)
type ToolReach struct {
	All   bool
	Words []string
}

// ToolReachesToolsFunc is a tool's ReachesTools: which tools a call with input may reach.
//
// upstream: types.ts:621 (reachesTools)
type ToolReachesToolsFunc func(input map[string]any) ToolReach

// dispatchReachesTools answers the host's tool_reaches_tools request with the named tool's ReachesTools: the string "all", or the array of words.
//
// upstream: types.ts:621 (reachesTools)
func (e *Extension) dispatchReachesTools(tool string, args json.RawMessage) (result any, failure error) {
	// upstream: a throwing reachesTools is the extension's error; a panic is the Go hook's throw.
	defer func() {
		if recovered := recover(); recovered != nil {
			result = nil
			if err, ok := recovered.(error); ok {
				failure = err
			} else {
				failure = fmt.Errorf("%v", recovered)
			}
		}
	}()
	e.toolMu.RLock()
	reaches := e.toolReachFuncs[tool]
	e.toolMu.RUnlock()
	if reaches == nil {
		return nil, fmt.Errorf("tool %s has no reachesTools", tool)
	}
	var input map[string]any
	if len(args) > 0 {
		if err := json.Unmarshal(args, &input); err != nil {
			return nil, fmt.Errorf("decode tool arguments: %w", err)
		}
	}
	if input == nil {
		input = map[string]any{}
	}
	reach := reaches(input)
	if reach.All {
		return "all", nil
	}
	if reach.Words == nil {
		return []string{}, nil
	}
	return reach.Words, nil
}

// dispatchPrepareLoadout answers the host's tool_prepare_loadout request with the changes the named tool's PrepareLoadout makes, or nothing.
//
// upstream: types.ts:601-607 (prepareLoadout)
func (e *Extension) dispatchPrepareLoadout(tool string, args json.RawMessage) (any, error) {
	e.toolMu.RLock()
	prepare := e.toolLoadoutFuncs[tool]
	e.toolMu.RUnlock()
	if prepare == nil {
		return nil, fmt.Errorf("tool %s has no prepareLoadout", tool)
	}
	var payload struct {
		Declared   []AgentTool               `json:"declared"`
		Callable   []AgentTool               `json:"callable"`
		Registered []AgentTool               `json:"registered"`
		Exposures  map[string]ToolExposure   `json:"exposures"`
		Namespaces map[string]*ToolNamespace `json:"namespaces"`
		Guidelines map[string][]string       `json:"promptGuidelines"`
	}
	if err := json.Unmarshal(args, &payload); err != nil {
		return nil, fmt.Errorf("decode tool loadout: %w", err)
	}
	changes := prepare(ToolLoadout{
		Declared: payload.Declared, Callable: payload.Callable, Registered: payload.Registered,
		// upstream: agent-session.ts:1480-1482 (an unknown tool is `direct`).
		GetExposure: func(name string) ToolExposure {
			if exposure, ok := payload.Exposures[name]; ok && exposure != "" {
				return exposure
			}
			return ToolExposureDirect
		},
		GetNamespace: func(name string) *ToolNamespace { return payload.Namespaces[name] },
		// upstream: agent-session.ts getPromptGuidelines: (name) => this._toolPromptGuidelines.get(name) ?? []
		GetPromptGuidelines: func(name string) []string { return payload.Guidelines[name] },
	})
	if changes == nil {
		return nil, nil
	}
	return changes, nil
}
