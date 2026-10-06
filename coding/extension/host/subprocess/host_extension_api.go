package subprocess

// Ports packages/coding-agent/src/core/extensions/loader.ts (registerMcpServer, unregisterMcpServer, registerVirtualModel, unregisterVirtualModel).
// Ports packages/coding-agent/src/core/extensions/runner.ts (createToolContext executeTool).
// Ports packages/coding-agent/src/core/virtual-models.ts (the router call).

import (
	"bytes"
	"context"
	stdjson "encoding/json"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// nestedCalls tracks the executeTool calls of each connection from the moment the read loop queues them until they finish, so the extension's own signal can cancel one. An SDK sends executeTool under the calling tool's request and executeTool.cancel outside it, in another call lane, so the cancel can run before the call reaches its handler. The call is reserved when the read loop queues its frame, which precedes the cancel's frame, and the reservation carries the cancellation until the handler starts. A call is named by the connection that made it and its ExecuteID.
type nestedCalls struct {
	mu    sync.Mutex
	calls map[nestedCallKey]*nestedCall
}

type nestedCallKey struct {
	conn *Conn
	id   string
}

// nestedCall is one reserved executeTool call. signal ends when the extension cancels the call.
type nestedCall struct {
	signal context.Context
	cancel context.CancelFunc
}

// reserveNestedCall reserves call when it is an executeTool call and returns the function that ends the reservation after the call ran or was dropped. Other calls get a no-op.
func (h *Host) reserveNestedCall(conn *Conn, call *CallPayload) (release func()) {
	if call.Method != CallExecuteTool {
		return func() {}
	}
	var named struct {
		ExecuteID string `json:"executeId"`
	}
	if json.Unmarshal(call.Args, &named) != nil {
		return func() {}
	}
	return h.nested.reserve(conn, named.ExecuteID)
}

func (n *nestedCalls) reserve(conn *Conn, id string) (release func()) {
	key := nestedCallKey{conn, id}
	signal, cancel := context.WithCancel(context.Background())
	reserved := &nestedCall{signal: signal, cancel: cancel}
	n.mu.Lock()
	if n.calls == nil {
		n.calls = make(map[nestedCallKey]*nestedCall)
	}
	n.calls[key] = reserved
	n.mu.Unlock()
	return func() {
		n.mu.Lock()
		if n.calls[key] == reserved {
			delete(n.calls, key)
		}
		n.mu.Unlock()
		cancel()
	}
}

// signal returns the cancellation of the reserved call, or nil when the call was not reserved.
func (n *nestedCalls) signal(conn *Conn, id string) context.Context {
	n.mu.Lock()
	defer n.mu.Unlock()
	if reserved := n.calls[nestedCallKey{conn, id}]; reserved != nil {
		return reserved.signal
	}
	return nil
}

func (n *nestedCalls) cancel(conn *Conn, id string) {
	n.mu.Lock()
	reserved := n.calls[nestedCallKey{conn, id}]
	n.mu.Unlock()
	if reserved != nil {
		reserved.cancel()
	}
}

// ── Registration ─────────────────────────────────────────────────────────────

// registerExtensionAPI applies the MCP servers and virtual models an extension registered while its factory ran. Upstream defers them until the factory succeeds and drops them when it throws, so a failure here leaves none of this extension's registrations behind.
//
// The entries apply in the order the extension registered them, each through the runtime's own checks: an MCP server is validated by name and config and a repeated name replaces the extension's earlier entry in place (loader.ts:456-468), and a virtual model queued before the bind is checked when the bind flushes it, which reports a failure as a `register_virtual_model` error without failing the load (runner.ts:497-513).
//
// A virtual model the extension unregistered while loading is removed from the runtime-wide queue, including another extension's: Pi's unregisterVirtualModel filters that queue (loader.ts:228-232), and the SDK already dropped this extension's own queued entry, so applying every unregistration before the virtual model registrations gives Pi's result for any call order. The unregistrations wait for the MCP servers: a server the host rejects is Pi's throw from registerMcpServer inside the factory, which discards every queued change (loader.ts:522-527, 604-608), and a filtered queue could not be restored.
//
// upstream: loader.ts:228-232, 259-262 (applyRuntimeChange), 456-497, 512-527 (commit, discard)
func (h *Host) registerExtensionAPI(me *managedExt, reg *RegisterPayload) error {
	path := extConfigOrigin(me.config)
	for _, server := range reg.McpServers {
		if err := h.providerRuntime.RegisterMcpServer(path, server.Name, server.Config); err != nil {
			h.releaseExtensionAPI(me, nil)
			return err
		}
		h.trackMcpServer(me, server.Name)
	}
	for _, ref := range reg.UnregisterVirtualModels {
		h.providerRuntime.UnregisterVirtualModel(ref.Provider, ref.ID)
		h.untrackVirtualModel(me, ref)
	}
	for _, decl := range reg.VirtualModels {
		if err := h.providerRuntime.RegisterVirtualModel(h.virtualModelDefinition(me, decl), path); err != nil {
			h.releaseExtensionAPI(me, nil)
			return err
		}
		h.trackVirtualModel(me, VirtualModelRef{Provider: decl.Provider, ID: decl.ID})
	}
	return nil
}

func (h *Host) trackMcpServer(me *managedExt, name string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !slices.Contains(me.mcpServerNames, name) {
		me.mcpServerNames = append(me.mcpServerNames, name)
	}
}

func (h *Host) untrackMcpServer(me *managedExt, name string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	me.mcpServerNames = slices.DeleteFunc(me.mcpServerNames, func(n string) bool { return n == name })
}

// trackVirtualModel records that me registered ref. Registering the same provider and id again replaces the virtual model (model-runtime.ts:939-952) whichever extension registers it, so me becomes its owner and an earlier registrant's stop leaves it.
func (h *Host) trackVirtualModel(me *managedExt, ref VirtualModelRef) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !slices.Contains(me.virtualModels, ref) {
		me.virtualModels = append(me.virtualModels, ref)
	}
	if h.virtualModelOwners == nil {
		h.virtualModelOwners = make(map[VirtualModelRef]*managedExt)
	}
	h.virtualModelOwners[ref] = me
}

// untrackVirtualModel records an unregistration. Pi's unregisterVirtualModel removes the model whoever registered it (model-runtime.ts:963-970), so nobody owns it afterwards.
func (h *Host) untrackVirtualModel(me *managedExt, ref VirtualModelRef) {
	h.mu.Lock()
	defer h.mu.Unlock()
	me.virtualModels = slices.DeleteFunc(me.virtualModels, func(r VirtualModelRef) bool { return r == ref })
	delete(h.virtualModelOwners, ref)
}

// releaseExtensionAPI removes the registrations of an extension that stopped. A replacement generation that registered the same server or virtual model keeps it: reload registers the successor before it stops its predecessor. A virtual model another extension registered after this one is that extension's and stays.
func (h *Host) releaseExtensionAPI(me, successor *managedExt) {
	h.mu.Lock()
	servers := slices.Clone(me.mcpServerNames)
	var models []VirtualModelRef
	for _, ref := range me.virtualModels {
		if h.virtualModelOwners[ref] == me {
			models = append(models, ref)
			delete(h.virtualModelOwners, ref)
		}
	}
	me.mcpServerNames, me.virtualModels = nil, nil
	var keepServers []string
	var keepModels []VirtualModelRef
	if successor != nil && successor != me {
		keepServers, keepModels = successor.mcpServerNames, successor.virtualModels
	}
	h.mu.Unlock()
	path := extConfigOrigin(me.config)
	for _, name := range servers {
		if !slices.Contains(keepServers, name) {
			h.providerRuntime.UnregisterMcpServer(path, name)
		}
	}
	for _, ref := range models {
		if !slices.Contains(keepModels, ref) {
			h.providerRuntime.UnregisterVirtualModel(ref.Provider, ref.ID)
		}
	}
}

// virtualModelDefinition builds the definition an extension declared. Its route runs in the extension.
func (h *Host) virtualModelDefinition(me *managedExt, decl VirtualModelDecl) extension.VirtualModelDefinition {
	levels := make([]ai.ModelThinkingLevel, len(decl.ThinkingLevels))
	for i, level := range decl.ThinkingLevels {
		levels[i] = ai.ModelThinkingLevel(level)
	}
	return extension.VirtualModelDefinition{
		Provider: decl.Provider, ID: decl.ID, Name: decl.Name,
		ThinkingLevels: levels, ContextWindow: decl.ContextWindow, MaxTokens: decl.MaxTokens, Input: slices.Clone(decl.Input),
		Route: h.makeVirtualModelRoute(me, VirtualModelRef{Provider: decl.Provider, ID: decl.ID}),
	}
}

// ── Calls ────────────────────────────────────────────────────────────────────

// handleMcpServerCall serves CallRegisterMcpServer and CallUnregisterMcpServer. The result lists every registered server, so the SDK's replicated getMcpServers reflects the change before the call returns.
//
// upstream: loader.ts:456-478
func (h *Host) handleMcpServerCall(_ context.Context, me *managedExt, call *CallPayload) (*CallResultPayload, error) {
	if me.shuttingDown.Load() {
		return nil, errors.New("extension was replaced")
	}
	path := extConfigOrigin(me.config)
	if call.Method == CallRegisterMcpServer {
		var decl McpServerDecl
		if err := json.Unmarshal(call.Args, &decl); err != nil {
			return nil, fmt.Errorf("decode %s: %w", call.Method, err)
		}
		if err := h.providerRuntime.RegisterMcpServer(path, decl.Name, decl.Config); err != nil {
			return nil, err
		}
		h.trackMcpServer(me, decl.Name)
	} else {
		var ref McpServerRef
		if err := json.Unmarshal(call.Args, &ref); err != nil {
			return nil, fmt.Errorf("decode %s: %w", call.Method, err)
		}
		h.providerRuntime.UnregisterMcpServer(path, ref.Name)
		h.untrackMcpServer(me, ref.Name)
	}
	result, err := json.Marshal(McpServersResult{Servers: h.providerRuntime.McpServers()})
	return &CallResultPayload{Result: result}, err
}

// handleMcpServersRead serves CallGetMcpServers: the live registry, which a Node factory reads before its register frame (loader.ts:475-478).
func (h *Host) handleMcpServersRead() (*CallResultPayload, error) {
	result, err := json.Marshal(McpServersResult{Servers: h.providerRuntime.McpServers()})
	return &CallResultPayload{Result: result}, err
}

// handleLoadingExec serves CallLoadingExec: upstream's execCommand in the working directory the loader was given, which a factory's pi.exec uses before any session binds (loader.ts:411-414).
func (h *Host) handleLoadingExec(ctx context.Context, call *CallPayload) (*CallResultPayload, error) {
	var request struct {
		Command string                 `json:"command"`
		Args    []string               `json:"args"`
		Options *extension.ExecOptions `json:"options,omitempty"`
	}
	if err := json.Unmarshal(call.Args, &request); err != nil {
		return nil, fmt.Errorf("parse exec args: %w", err)
	}
	result, err := extension.ExecCommand(ctx, h.cwd, request.Command, request.Args, request.Options)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	return &CallResultPayload{Result: data}, nil
}

// handleMcpServerCheck serves CallCheckMcpServer: the validation and ownership checks of registerMcpServer, which throw to the factory when the call is made (loader.ts:456-468). The registration itself waits for the register frame.
func (h *Host) handleMcpServerCheck(me *managedExt, call *CallPayload) (*CallResultPayload, error) {
	var decl McpServerDecl
	if err := json.Unmarshal(call.Args, &decl); err != nil {
		return nil, fmt.Errorf("decode %s: %w", call.Method, err)
	}
	if _, err := h.providerRuntime.CheckMcpServer(extConfigOrigin(me.config), decl.Name, decl.Config); err != nil {
		return nil, err
	}
	return &CallResultPayload{}, nil
}

// handleVirtualModelCall serves CallRegisterVirtualModel and CallUnregisterVirtualModel.
//
// upstream: loader.ts:480-497
func (h *Host) handleVirtualModelCall(_ context.Context, me *managedExt, call *CallPayload) (*CallResultPayload, error) {
	if me.shuttingDown.Load() {
		return nil, errors.New("extension was replaced")
	}
	if call.Method == CallRegisterVirtualModel {
		var decl VirtualModelDecl
		if err := json.Unmarshal(call.Args, &decl); err != nil {
			return nil, fmt.Errorf("decode %s: %w", call.Method, err)
		}
		if err := h.providerRuntime.RegisterVirtualModel(h.virtualModelDefinition(me, decl), extConfigOrigin(me.config)); err != nil {
			return nil, err
		}
		h.trackVirtualModel(me, VirtualModelRef{Provider: decl.Provider, ID: decl.ID})
		return &CallResultPayload{}, nil
	}
	var ref VirtualModelRef
	if err := json.Unmarshal(call.Args, &ref); err != nil {
		return nil, fmt.Errorf("decode %s: %w", call.Method, err)
	}
	h.providerRuntime.UnregisterVirtualModel(ref.Provider, ref.ID)
	h.untrackVirtualModel(me, ref)
	return &CallResultPayload{}, nil
}

// nestedCaller identifies the executeTool call that made a nested call: the connection it came on and its id there.
type nestedCaller struct {
	conn      *Conn
	executeID string
}

type nestedCallerKey struct{}

// handleExecuteToolCall serves CallExecuteTool and CallExecuteToolCancel. A nested call runs under the call's context, which ends with the calling request, and under the extension's own cancel. Partial results reach the extension as requests, in order, before the outcome; an extension that throws from its callback rejects the call with the first error (nested-tool-calls.ts:219-248).
//
// upstream: runner.ts:966-983
func (h *Host) handleExecuteToolCall(ctx context.Context, conn *Conn, call *CallPayload) (*CallResultPayload, error) {
	if call.Method == CallExecuteToolCancel {
		var cancel ExecuteToolCancel
		if err := json.Unmarshal(call.Args, &cancel); err != nil {
			return nil, fmt.Errorf("decode %s: %w", call.Method, err)
		}
		h.nested.cancel(conn, cancel.ExecuteID)
		return &CallResultPayload{}, nil
	}
	var args ExecuteToolArgs
	if err := json.Unmarshal(call.Args, &args); err != nil {
		return nil, fmt.Errorf("decode %s: %w", call.Method, err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	// runner.ts:979-981: the nested tool runs with the extension's signal when it passed one, which its runtime hands the tool as its signal parameter.
	if args.OwnSignal {
		runCtx = context.WithValue(extension.WithOwnSignal(runCtx), nestedCallerKey{}, nestedCaller{conn: conn, executeID: args.ExecuteID})
	}
	if signal := h.nested.signal(conn, args.ExecuteID); signal != nil {
		stop := context.AfterFunc(signal, cancel)
		defer stop()
	}
	// The extension's next call, including the cancel, applies after this call started.
	extension.CallInitiated(ctx)

	var execute func(context.Context, string, string, json.RawMessage, extension.ExecuteToolOptions) (extension.AgentToolCallOutcome, error)
	if h.uiBridge != nil {
		if actions := h.uiBridge.hostCallbacks(); actions != nil {
			execute = actions.ExecuteTool
		}
	}
	var outcome extension.AgentToolCallOutcome
	if execute == nil {
		outcome = UnavailableNestedCall(args.CallerID, args.Name)
	} else {
		var executeErr error
		var options extension.ExecuteToolOptions
		var open sync.Mutex
		finished := false
		if args.WantsUpdates {
			// The update waits for the extension's callback, as Pi's runs in the tool's tick, and an error answer is the callback's throw. The request outlives the nested call's cancellation: Pi's callback has none.
			updateCtx := context.WithoutCancel(runCtx)
			options.OnUpdate = func(partial agent.AgentToolResult) error {
				open.Lock()
				defer open.Unlock()
				if finished {
					return nil
				}
				payload, err := json.Marshal(ExecuteToolUpdate{ExecuteID: args.ExecuteID, Result: agentToolResultWire(partial)})
				if err != nil {
					return err
				}
				resp, err := conn.Request(updateCtx, &Envelope{Type: MsgRequest, Request: &RequestPayload{Method: RequestExecuteToolUpdate, Args: payload}})
				if err != nil {
					return err
				}
				if resp.Response != nil && resp.Response.Error != nil {
					return errors.New(resp.Response.Error.Message)
				}
				return nil
			}
		}
		outcome, executeErr = execute(runCtx, args.CallerID, args.Name, args.Args, options)
		open.Lock()
		finished = true
		open.Unlock()
		if executeErr != nil {
			return nil, executeErr
		}
	}
	result, err := json.Marshal(ExecuteToolOutcome{ToolCall: outcome.ToolCall, Result: agentToolResultWire(outcome.Result), IsError: outcome.IsError})
	return &CallResultPayload{Result: result}, err
}

// UnavailableNestedCall is upstream's outcome when no executeTool action is bound: an error whose call id is `<caller>/0`. A mode that binds the action to a session it may not have yet answers with it too.
//
// upstream: runner.ts:968-976
func UnavailableNestedCall(callerID, name string) extension.AgentToolCallOutcome {
	return extension.AgentToolCallOutcome{
		ToolCall: ai.ToolCall{ID: callerID + "/0", Name: name, Arguments: ai.JsonObject{}},
		Result: agent.AgentToolResult{
			Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "Nested tool calls are not available in this context"}},
			Details: map[string]any{},
		},
		IsError: true,
	}
}

// agentToolResultWire encodes a tool result in Pi's AgentToolResult shape (content, details, structuredContent, isError, usage, terminate), with its members in the order the object holds them (agent.AgentToolResult.MemberNames).
//
// upstream: nested-tool-calls.ts:220-245 and agent-loop.ts:810-818 (runToolCall) hand on the called tool's own object.
func agentToolResultWire(result any) json.RawMessage {
	switch typed := result.(type) {
	case agent.AgentToolResult:
		out := []byte{'{'}
		for _, name := range typed.MemberNames() {
			var value any
			switch name {
			case "content":
				content := typed.Content
				if content == nil {
					content = []ai.ToolResultMessageContent{}
				}
				value = content
			case "details":
				value = typed.Details
			case "structuredContent":
				value = json.RawMessage(typed.StructuredContent)
			case "isError":
				value = typed.IsError
			case "terminate":
				value = typed.Terminate
			case "usage":
				value = typed.Usage
			}
			encoded, err := json.Marshal(value)
			if err != nil {
				out = nil
				break
			}
			if len(out) > 1 {
				out = append(out, ',')
			}
			out = append(out, '"')
			out = append(out, name...)
			out = append(out, '"', ':')
			out = append(out, encoded...)
		}
		if out != nil {
			return append(out, '}')
		}
	case nil:
	default:
		if encoded, err := json.Marshal(typed); err == nil {
			return encoded
		}
	}
	return json.RawMessage(`{"content":[],"details":{}}`)
}

// ── Requests to the extension ────────────────────────────────────────────────

// makeVirtualModelRoute returns the router of a virtual model that runs in the extension. The request is a RequestVirtualModelRoute; the route names the physical model by provider and id, and the model runtime resolves it (coding.ModelRuntime.ResolveModel) as upstream does.
//
// upstream: loader.ts:485-487, virtual-models.ts:100
func (h *Host) makeVirtualModelRoute(me *managedExt, ref VirtualModelRef) extension.ModelRouteFunc {
	return func(ctx context.Context, request extension.ModelRouteRequest) (extension.ModelRoute, error) {
		me := me.current()
		conn := me.connection()
		if conn == nil {
			return extension.ModelRoute{}, errors.New("extension not connected")
		}
		wire, err := virtualModelRouteRequest(request)
		if err != nil {
			return extension.ModelRoute{}, err
		}
		args, err := json.Marshal(VirtualModelRouteArgs{VirtualModelRef: ref, Request: wire})
		if err != nil {
			return extension.ModelRoute{}, err
		}
		reqCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		if err := h.pushStateTo(reqCtx, me, conn); err != nil {
			return extension.ModelRoute{}, fmt.Errorf("sync extension state for virtual model %s/%s: %w", ref.Provider, ref.ID, err)
		}
		resp, err := conn.Request(reqCtx, &Envelope{Type: MsgRequest, Request: &RequestPayload{Method: RequestVirtualModelRoute, Args: args}})
		if err != nil {
			return extension.ModelRoute{}, fmt.Errorf("virtual model %s/%s: %w", ref.Provider, ref.ID, err)
		}
		if resp.Response != nil && resp.Response.Error != nil {
			return extension.ModelRoute{}, resp.Response.Error.ToError()
		}
		var routed VirtualModelRouteResult
		if resp.Response == nil || json.Unmarshal(resp.Response.Result, &routed) != nil {
			return extension.ModelRoute{}, fmt.Errorf("virtual model %s/%s: the router returned no route", ref.Provider, ref.ID)
		}
		provider, _ := routed.Model["provider"].(string)
		id, _ := routed.Model["id"].(string)
		// upstream: model-runtime.ts:1006-1009: the router's model goes back as returned, naming its provider and id, and the model runtime resolves it to a physical catalog model or rejects it with `<name> routed to <provider>/<id>, which is not a physical model.`
		model := &ai.Model{ID: id, ProviderMeta: ai.ProviderMetadata{ProviderID: provider}}
		return extension.ModelRoute{Model: model, ThinkingLevel: ai.ModelThinkingLevel(routed.ThinkingLevel), State: routeState(request.State, routed, me.hostsNodeRuntime())}, nil
	}
}

func virtualModelRouteRequest(request extension.ModelRouteRequest) (VirtualModelRouteRequest, error) {
	wire := VirtualModelRouteRequest{
		Model: extension.ModelInfo(request.Model), ThinkingLevel: string(request.ThinkingLevel), Reason: string(request.Reason),
		State: request.State, Messages: make([]json.RawMessage, len(request.Messages)),
	}
	if request.Previous != nil {
		wire.Previous = &VirtualModelRoutePrevious{Model: extension.ModelInfo(request.Previous.Model), ThinkingLevel: string(request.Previous.ThinkingLevel)}
	}
	if request.Failed != nil {
		message, err := json.Marshal(request.Failed.Message)
		if err != nil {
			return wire, err
		}
		wire.Failed = &VirtualModelRouteFailed{Model: extension.ModelInfo(request.Failed.Model), ThinkingLevel: string(request.Failed.ThinkingLevel), Message: message}
	}
	for i, message := range request.Messages {
		encoded, err := json.Marshal(message)
		if err != nil {
			return wire, err
		}
		wire.Messages[i] = encoded
	}
	return wire, nil
}

// makeToolPrepareLoadout returns a tool's prepareLoadout, which runs in the extension. A null answer, or a request the connection could not deliver, changes nothing. An error the extension answers is the hook's throw: it panics with that error, which the session reports as a `prepare_loadout` extension error ([extension.ToolPrepareLoadoutFunc]).
//
// upstream: types.ts:601-607, agent-session.ts:1520-1533
func (h *Host) makeToolPrepareLoadout(me *managedExt, toolName string) extension.ToolPrepareLoadoutFunc {
	return func(loadout extension.ToolLoadout) *extension.ToolLoadoutChanges {
		me := me.current()
		conn := me.connection()
		if conn == nil {
			return nil
		}
		payload := ToolLoadoutPayload{
			Declared: loadout.Declared, Callable: loadout.Callable, Registered: loadout.Registered,
			Exposures: make(map[string]extension.ToolExposure, len(loadout.Registered)),
		}
		for _, tool := range loadout.Registered {
			exposure := extension.ToolExposureDirect
			if loadout.GetExposure != nil {
				exposure = loadout.GetExposure(tool.Name)
			}
			payload.Exposures[tool.Name] = exposure
			if loadout.GetNamespace != nil {
				if namespace := loadout.GetNamespace(tool.Name); namespace != nil {
					if payload.Namespaces == nil {
						payload.Namespaces = make(map[string]*extension.ToolNamespace)
					}
					payload.Namespaces[tool.Name] = namespace
				}
			}
		}
		args, err := json.Marshal(payload)
		if err != nil {
			return nil
		}
		ctx := me.parentCtx
		if ctx == nil {
			ctx = context.Background()
		}
		if err := h.pushStateTo(ctx, me, conn); err != nil {
			return nil
		}
		resp, err := conn.Request(ctx, &Envelope{Type: MsgRequest, Request: &RequestPayload{Method: RequestPrepareLoadout, Tool: toolName, Args: args}})
		if err != nil || resp.Response == nil {
			return nil
		}
		if resp.Response.Error != nil {
			panic(resp.Response.Error.ToError())
		}
		var changes *extension.ToolLoadoutChanges
		if json.Unmarshal(resp.Response.Result, &changes) != nil {
			return nil
		}
		return changes
	}
}

// routeState is the state the session compares with the one it sent. Upstream stores a router's state unless `route.state === request.state` (agent-session.ts:788), which is object identity. Node observes it in its own process and says so, so a Node router's fresh object stays a new state even when it holds the request state's values. The other runtimes return a value and cannot express identity, so the host compares the JSON values. Either way an unchanged state comes back as the request's own bytes: the bytes that crossed the process boundary are the encoder's (HTML escapes, key order, spacing) and say nothing about identity.
func routeState(sent json.RawMessage, routed VirtualModelRouteResult, observesIdentity bool) json.RawMessage {
	if routed.State == nil {
		return nil
	}
	if routed.StateUnchanged || (!observesIdentity && jsonValuesEqual(sent, routed.State)) {
		return sent
	}
	return routed.State
}

// jsonValuesEqual reports whether two JSON documents hold the same value, whatever their encoding. An absent document equals nothing.
func jsonValuesEqual(a, b json.RawMessage) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	var left, right any
	for _, pair := range []struct {
		raw  json.RawMessage
		into *any
	}{{a, &left}, {b, &right}} {
		decoder := stdjson.NewDecoder(bytes.NewReader(pair.raw))
		decoder.UseNumber()
		if decoder.Decode(pair.into) != nil {
			return false
		}
	}
	return jsonEqual(left, right)
}

func jsonEqual(a, b any) bool {
	switch left := a.(type) {
	case stdjson.Number:
		right, ok := b.(stdjson.Number)
		if !ok {
			return false
		}
		x, okX := new(big.Rat).SetString(left.String())
		y, okY := new(big.Rat).SetString(right.String())
		return okX && okY && x.Cmp(y) == 0
	case []any:
		right, ok := b.([]any)
		return ok && slices.EqualFunc(left, right, jsonEqual)
	case map[string]any:
		right, ok := b.(map[string]any)
		if !ok || len(left) != len(right) {
			return false
		}
		for key, value := range left {
			other, present := right[key]
			if !present || !jsonEqual(value, other) {
				return false
			}
		}
		return true
	default:
		return a == b
	}
}
