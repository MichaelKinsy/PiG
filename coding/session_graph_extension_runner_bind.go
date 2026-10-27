package coding

// The binding of the ExtensionRunner graph object: Pi's bindCore, bindCommandContext and setUIContext take functions of the host process (`ExtensionActions`, `ExtensionContextActions`, `ExtensionCommandContextActions`, the provider actions, an `ExtensionUIContext`), which here are the extension's functions named as session graph callbacks. They bind into the runner, and into the UI bridges of the loaders whose extensions the runner dispatches to, which is where those extensions' own contexts read the same state.

import (
	"context"
	"errors"
	"fmt"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// callbackIDs is a Pi action object as it crosses the wire: the name of each function the extension supplied.
type callbackIDs map[string]string

// run runs the function the extension supplied under name and answers its JSON result; it reports false when the extension supplied none.
func (g *GraphExtensionRunner) run(ctx context.Context, ids callbackIDs, name string, args ...any) (json.RawMessage, bool, error) {
	id := ids[name]
	if id == "" {
		return nil, false, nil
	}
	raw, err := g.callback(ctx, id, args...)
	return raw, true, err
}

// runDecoded is run with the result decoded as a T; a function that returned nothing is T's zero value.
func runDecoded[T graphDecoded](g *GraphExtensionRunner, ctx context.Context, ids callbackIDs, name string, args ...any) (T, error) {
	raw, _, err := g.run(ctx, ids, name, args...)
	if err != nil {
		var zero T
		return zero, err
	}
	return decodeJSON[T](raw)
}

// runVoid runs a function whose Pi result is void, and reports its error.
func (g *GraphExtensionRunner) runVoid(ctx context.Context, ids callbackIDs, name string, args ...any) error {
	_, _, err := g.run(ctx, ids, name, args...)
	return err
}

func wireModel(model *ai.Model) (any, error) {
	if model == nil {
		return nil, nil
	}
	raw, err := extension.ModelObject(model)
	return raw, err
}

// graphModelOf is the model object an extension's function returned, nil for undefined.
func graphModelOf(raw json.RawMessage) (*ai.Model, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	return icodingagent.GraphAPIModel(raw)
}

// extensionActions is Pi's ExtensionActions over the extension's functions.
func (g *GraphExtensionRunner) extensionActions(ids callbackIDs) extension.ExtensionActions {
	bg := callbackBackground
	has := func(name string) bool { return ids[name] != "" }
	var actions extension.ExtensionActions
	if has("sendMessage") {
		actions.SendMessage = func(message extension.CustomMessageRef, options *extension.SendMessageOptions) error {
			return g.runVoid(bg(), ids, "sendMessage", message, options)
		}
	}
	if has("sendUserMessage") {
		actions.SendUserMessage = func(content any, options *extension.SendUserMessageOptions) error {
			return g.runVoid(bg(), ids, "sendUserMessage", content, options)
		}
	}
	if has("appendEntry") {
		actions.AppendEntry = func(customType string, data any) error {
			return g.runVoid(bg(), ids, "appendEntry", customType, data)
		}
	}
	if has("setSessionName") {
		actions.SetSessionName = func(name string) error { return g.runVoid(bg(), ids, "setSessionName", name) }
	}
	if has("getSessionName") {
		actions.GetSessionName = func() string {
			name, _ := runDecoded[string](g, bg(), ids, "getSessionName")
			return name
		}
	}
	if has("setLabel") {
		actions.SetLabel = func(entryID string, label *string) error { return g.runVoid(bg(), ids, "setLabel", entryID, label) }
	}
	if has("getActiveTools") {
		actions.GetActiveTools = func() []string {
			names, _ := runDecoded[[]string](g, bg(), ids, "getActiveTools")
			return nonNilSlice(names)
		}
	}
	if has("getAllTools") {
		actions.GetAllTools = func() []extension.ToolInfo {
			tools, _ := runDecoded[[]extension.ToolInfo](g, bg(), ids, "getAllTools")
			return nonNilSlice(tools)
		}
	}
	if has("getSettings") {
		actions.GetSettings = func() extension.Settings {
			settings, _ := runDecoded[extension.Settings](g, bg(), ids, "getSettings")
			return settings
		}
	}
	if has("setActiveTools") {
		actions.SetActiveTools = func(names []string) { _ = g.runVoid(bg(), ids, "setActiveTools", nonNilSlice(names)) }
	}
	if has("refreshTools") {
		actions.RefreshTools = func() error { return g.runVoid(bg(), ids, "refreshTools") }
	}
	if has("getCommands") {
		actions.GetCommands = func() []extension.SlashCommandInfo {
			commands, _ := runDecoded[[]extension.SlashCommandInfo](g, bg(), ids, "getCommands")
			return nonNilSlice(commands)
		}
	}
	if has("setModel") {
		actions.SetModel = func(ctx context.Context, model extension.Model) (bool, error) {
			wire, err := wireModel(model)
			if err != nil {
				return false, err
			}
			return runDecoded[bool](g, ctx, ids, "setModel", wire)
		}
	}
	if has("getThinkingLevel") {
		actions.GetThinkingLevel = func() extension.ThinkingLevel {
			level, _ := runDecoded[extension.ThinkingLevel](g, bg(), ids, "getThinkingLevel")
			return level
		}
	}
	if has("setThinkingLevel") {
		actions.SetThinkingLevel = func(level extension.ThinkingLevel) { _ = g.runVoid(bg(), ids, "setThinkingLevel", level) }
	}
	return actions
}

// contextActions is Pi's ExtensionContextActions over the extension's functions.
func (g *GraphExtensionRunner) contextActions(ids callbackIDs) extension.ContextActions {
	bg := callbackBackground
	has := func(name string) bool { return ids[name] != "" }
	var actions extension.ContextActions
	if has("getModel") {
		actions.GetModel = func() extension.Model {
			raw, _, err := g.run(bg(), ids, "getModel")
			if err != nil {
				return nil
			}
			model, _ := graphModelOf(raw)
			return model
		}
	}
	if has("getScopedModels") {
		actions.GetScopedModels = func() []extension.ScopedModel {
			entries, err := runDecoded[[]graphScopedModel](g, bg(), ids, "getScopedModels")
			if err != nil {
				return []extension.ScopedModel{}
			}
			out := make([]extension.ScopedModel, 0, len(entries))
			for _, entry := range entries {
				model, err := graphModelOf(entry.Model)
				if err != nil || model == nil {
					continue
				}
				out = append(out, extension.ScopedModel{Model: model, ThinkingLevel: extension.ThinkingLevel(entry.ThinkingLevel)})
			}
			return out
		}
	}
	if has("isIdle") {
		actions.IsIdle = func() bool {
			idle, _ := runDecoded[bool](g, bg(), ids, "isIdle")
			return idle
		}
	}
	if has("isProjectTrusted") {
		actions.IsProjectTrusted = func() bool {
			trusted, _ := runDecoded[bool](g, bg(), ids, "isProjectTrusted")
			return trusted
		}
	}
	if has("getSignal") {
		actions.GetSignal = func() context.Context {
			signal, err := runDecoded[*graphSignalState](g, bg(), ids, "getSignal")
			if err != nil || signal == nil {
				return nil
			}
			if !signal.Aborted {
				return bg()
			}
			ctx, cancel := context.WithCancel(bg())
			cancel()
			return ctx
		}
	}
	if has("abort") {
		actions.Abort = func() { _ = g.runVoid(bg(), ids, "abort") }
	}
	if has("hasPendingMessages") {
		actions.HasPendingMessages = func() bool {
			pending, _ := runDecoded[bool](g, bg(), ids, "hasPendingMessages")
			return pending
		}
	}
	if has("shutdown") {
		actions.Shutdown = func() { _ = g.runVoid(bg(), ids, "shutdown") }
	}
	if has("getContextUsage") {
		actions.GetContextUsage = func() *extension.ContextUsage {
			usage, _ := runDecoded[*extension.ContextUsage](g, bg(), ids, "getContextUsage")
			return usage
		}
	}
	if has("compact") {
		actions.Compact = func(opts *extension.CompactOptions) {
			wire := map[string]any{}
			if opts != nil && opts.CustomInstructions != "" {
				wire["customInstructions"] = opts.CustomInstructions
			}
			if err := g.runVoid(bg(), ids, "compact", wire); err != nil && opts != nil && opts.OnError != nil {
				opts.OnError(err)
			}
		}
	}
	if has("getSystemPrompt") {
		actions.GetSystemPrompt = func() string {
			prompt, _ := runDecoded[string](g, bg(), ids, "getSystemPrompt")
			return prompt
		}
	}
	if has("getSystemPromptOptions") {
		actions.GetSystemPromptOptions = func() *extension.BuildSystemPromptOptions {
			options, err := runDecoded[*extension.BuildSystemPromptOptions](g, bg(), ids, "getSystemPromptOptions")
			if err != nil || options == nil {
				return nil
			}
			return options
		}
	}
	if has("executeTool") {
		actions.ExecuteTool = func(ctx context.Context, callerID, name string, args json.RawMessage, options extension.ExecuteToolOptions) (extension.AgentToolCallOutcome, error) {
			return runDecoded[extension.AgentToolCallOutcome](g, ctx, ids, "executeTool", callerID, name, args, map[string]any{})
		}
	}
	if has("getCallableTools") {
		actions.GetCallableTools = func() []extension.AgentTool {
			tools, _ := runDecoded[[]extension.AgentTool](g, bg(), ids, "getCallableTools")
			return nonNilSlice(tools)
		}
	}
	return actions
}

// errOptionFunctions is the error of a session replacement whose options carry functions of the extension that asked for it: they run in that extension's process, which a runner over a loader's extensions does not reach.
var errOptionFunctions = errors.New("the session replacement options setup and withSession are functions of the extension that asked for the replacement, which the runner's bound handler cannot receive")

// commandActions is Pi's ExtensionCommandContextActions over the extension's functions.
func (g *GraphExtensionRunner) commandActions(ids callbackIDs) extension.CommandActions {
	has := func(name string) bool { return ids[name] != "" }
	var actions extension.CommandActions
	cancelled := func(ctx context.Context, name string, args ...any) (extension.CancelledResult, error) {
		return runDecoded[extension.CancelledResult](g, ctx, ids, name, args...)
	}
	if has("waitForIdle") {
		actions.WaitForIdleContext = func(ctx context.Context) error { return g.runVoid(ctx, ids, "waitForIdle") }
	}
	if has("newSession") {
		actions.NewSessionContext = func(ctx context.Context, opts *extension.NewSessionOptions) (extension.CancelledResult, error) {
			wire := map[string]any{}
			if opts != nil {
				if opts.Setup != nil || opts.WithSession != nil {
					return extension.CancelledResult{}, errOptionFunctions
				}
				if opts.ParentSession != "" {
					wire["parentSession"] = opts.ParentSession
				}
			}
			return cancelled(ctx, "newSession", wire)
		}
	}
	if has("fork") {
		actions.ForkContext = func(ctx context.Context, entryID string, opts *extension.ForkOptions) (extension.CancelledResult, error) {
			wire := map[string]any{}
			if opts != nil {
				if opts.WithSession != nil {
					return extension.CancelledResult{}, errOptionFunctions
				}
				if opts.Position != "" {
					wire["position"] = opts.Position
				}
			}
			return cancelled(ctx, "fork", entryID, wire)
		}
	}
	if has("navigateTree") {
		actions.NavigateTreeContext = func(ctx context.Context, targetID string, opts *extension.NavigateTreeOptions) (extension.CancelledResult, error) {
			return cancelled(ctx, "navigateTree", targetID, opts)
		}
	}
	if has("switchSession") {
		actions.SwitchSessionContext = func(ctx context.Context, path string, opts *extension.SwitchSessionOptions) (extension.CancelledResult, error) {
			if opts != nil && opts.WithSession != nil {
				return extension.CancelledResult{}, errOptionFunctions
			}
			return cancelled(ctx, "switchSession", path, map[string]any{})
		}
	}
	if has("reload") {
		actions.ReloadContext = func(ctx context.Context) error { return g.runVoid(ctx, ids, "reload") }
	}
	return actions
}

// bindCore is `bindCore(actions, contextActions, providerActions?)`.
func (g *GraphExtensionRunner) bindCore(args icodingagent.GraphArgs) error {
	var actionIDs callbackIDs
	err := args.Unmarshal(0, func(raw json.RawMessage) error { return json.Unmarshal(raw, &actionIDs) })
	if err != nil {
		return err
	}
	var contextIDs callbackIDs
	err = args.Unmarshal(1, func(raw json.RawMessage) error { return json.Unmarshal(raw, &contextIDs) })
	if err != nil {
		return err
	}
	var providerIDs callbackIDs
	err = args.Unmarshal(2, func(raw json.RawMessage) error { return json.Unmarshal(raw, &providerIDs) })
	if err != nil {
		return err
	}
	actions := g.extensionActions(actionIDs)
	contextActions := g.contextActions(contextIDs)
	var providers *extension.ProviderActions
	if len(providerIDs) != 0 {
		providers = &extension.ProviderActions{}
		if providerIDs["registerProvider"] != "" {
			providers.RegisterProvider = func(name string, config extension.ProviderConfig) error {
				return g.runVoid(callbackBackground(), providerIDs, "registerProvider", name, config)
			}
		}
		if providerIDs["unregisterProvider"] != "" {
			providers.UnregisterProvider = func(name string) { _ = g.runVoid(callbackBackground(), providerIDs, "unregisterProvider", name) }
		}
	}
	g.mu.Lock()
	g.boundContext = contextIDs
	g.mu.Unlock()
	g.runner.BindCore(actions, contextActions, providers)
	g.bindBridges(actionIDs, contextIDs)
	return nil
}

// bindCommandContext is `bindCommandContext(actions?)`: an omitted object restores Pi's defaults.
func (g *GraphExtensionRunner) bindCommandContext(args icodingagent.GraphArgs) error {
	var ids callbackIDs
	err := args.Unmarshal(0, func(raw json.RawMessage) error { return json.Unmarshal(raw, &ids) })
	if err != nil {
		return err
	}
	actions := g.commandActions(ids)
	if len(ids) == 0 {
		// Pi's defaults: waitForIdle and reload resolve, and a session replacement is not cancelled.
		actions = extension.CommandActions{
			WaitForIdle: func() error { return nil },
			NewSession: func(*extension.NewSessionOptions) (extension.CancelledResult, error) {
				return extension.CancelledResult{}, nil
			},
			Fork: func(string, *extension.ForkOptions) (extension.CancelledResult, error) {
				return extension.CancelledResult{}, nil
			},
			NavigateTree: func(string, *extension.NavigateTreeOptions) (extension.CancelledResult, error) {
				return extension.CancelledResult{}, nil
			},
			SwitchSession: func(string, *extension.SwitchSessionOptions) (extension.CancelledResult, error) {
				return extension.CancelledResult{}, nil
			},
			Reload: func() error { return nil },
		}
	}
	g.runner.BindCommandActions(actions)
	for _, bridge := range g.bridges {
		bridge.BindCommandActions(actions)
	}
	return nil
}

// bindBridges makes the state the extension's functions answer the state the loaded extensions' contexts read.
func (g *GraphExtensionRunner) bindBridges(actionIDs, contextIDs callbackIDs) {
	if len(g.bridges) == 0 {
		return
	}
	bg := callbackBackground
	actions, contextActions := g.extensionActions(actionIDs), g.contextActions(contextIDs)
	// loader.ts: every call of an extension's API starts with assertActive, so once the runner is invalidated the calls that can fail report its message.
	live := g.assertActive
	for _, bridge := range g.bridges {
		if actions.SendMessage != nil {
			bridge.SetHostAction("sendMessage", func(message extension.CustomMessageRef, opts subprocess.SendMessageOptions) error {
				if err := live(); err != nil {
					return err
				}
				return actions.SendMessage(message, &extension.SendMessageOptions{TriggerTurn: opts.TriggerTurn, DeliverAs: extension.DeliverAs(opts.DeliverAs)})
			})
		}
		if actions.SendUserMessage != nil {
			bridge.SetHostAction("sendUserMessage", func(content any, opts subprocess.SendUserMessageOptions) error {
				if err := live(); err != nil {
					return err
				}
				return actions.SendUserMessage(content, &extension.SendUserMessageOptions{DeliverAs: extension.DeliverAs(opts.DeliverAs)})
			})
		}
		if actions.AppendEntry != nil {
			bridge.SetHostAction("appendEntry", func(customType string, data any, _ *subprocess.DirectEntryAppend) error {
				if err := live(); err != nil {
					return err
				}
				return actions.AppendEntry(customType, data)
			})
		}
		if actions.SetSessionName != nil {
			bridge.SetHostAction("setSessionName", func(name string) error {
				if err := live(); err != nil {
					return err
				}
				return actions.SetSessionName(name)
			})
		}
		if actions.GetSessionName != nil {
			bridge.SetHostAction("getSessionName", actions.GetSessionName)
		}
		if actions.SetLabel != nil {
			bridge.SetHostAction("setLabel", func(entryID, label string) error {
				if err := live(); err != nil {
					return err
				}
				if label == "" {
					return actions.SetLabel(entryID, nil)
				}
				return actions.SetLabel(entryID, &label)
			})
		}
		if actions.GetActiveTools != nil {
			bridge.SetHostAction("getActiveTools", actions.GetActiveTools)
		}
		if actions.GetAllTools != nil {
			bridge.SetHostAction("getAllTools", func() []subprocess.ToolInfo {
				tools := actions.GetAllTools()
				out := make([]subprocess.ToolInfo, len(tools))
				for i, tool := range tools {
					out[i] = subprocess.ToolInfo{Name: tool.Name, Description: tool.Description, Parameters: tool.Parameters, PromptGuidelines: tool.PromptGuidelines, SourceInfo: tool.SourceInfo, Exposure: tool.Exposure, Namespace: tool.Namespace, Annotations: tool.Annotations}
				}
				return out
			})
		}
		if actions.GetSettings != nil {
			bridge.SetHostAction("getSettings", actions.GetSettings)
		}
		if actions.SetActiveTools != nil {
			bridge.SetHostAction("setActiveTools", actions.SetActiveTools)
		}
		if actions.RefreshTools != nil {
			bridge.SetHostAction("refreshTools", func() error {
				if err := live(); err != nil {
					return err
				}
				return actions.RefreshTools()
			})
		}
		if actions.GetCommands != nil {
			bridge.SetHostAction("getCommands", func() []subprocess.CommandInfo {
				commands := actions.GetCommands()
				out := make([]subprocess.CommandInfo, len(commands))
				for i, command := range commands {
					out[i] = subprocess.CommandInfo{Name: command.Name, Description: command.Description, Source: command.Source, SourceInfo: command.SourceInfo}
				}
				return out
			})
		}
		if actions.SetModel != nil {
			bridge.SetHostAction("setModel", func(ctx context.Context, spec string) (bool, error) {
				extension.CallInitiated(ctx)
				return g.runBridgeSetModel(ctx, actionIDs, spec)
			})
		}
		if actions.GetThinkingLevel != nil {
			bridge.SetHostAction("getThinkingLevel", func() string { return string(actions.GetThinkingLevel()) })
		}
		if actions.SetThinkingLevel != nil {
			bridge.SetHostAction("setThinkingLevel", func(level string) { actions.SetThinkingLevel(extension.ThinkingLevel(level)) })
		}
		if contextActions.GetModel != nil {
			bridge.SetHostAction("getModelInfo", func() json.RawMessage {
				raw, _, err := g.run(bg(), contextIDs, "getModel")
				if err != nil || len(raw) == 0 || string(raw) == "null" {
					return nil
				}
				return raw
			})
		}
		if contextActions.GetScopedModels != nil {
			bridge.SetHostAction("getScopedModels", contextActions.GetScopedModels)
		}
		if contextActions.IsIdle != nil {
			bridge.SetHostAction("isIdle", contextActions.IsIdle)
		}
		if contextActions.IsProjectTrusted != nil {
			bridge.SetHostAction("isProjectTrusted", contextActions.IsProjectTrusted)
		}
		if contextActions.GetSignal != nil {
			bridge.SetHostAction("getSignal", contextActions.GetSignal)
		}
		if contextActions.Abort != nil {
			bridge.SetHostAction("abort", contextActions.Abort)
		}
		if contextActions.HasPendingMessages != nil {
			bridge.SetHostAction("hasPendingMessages", contextActions.HasPendingMessages)
		}
		if contextActions.Shutdown != nil {
			bridge.SetHostAction("shutdown", contextActions.Shutdown)
		}
		if contextActions.GetContextUsage != nil {
			bridge.SetHostAction("getContextUsage", contextActions.GetContextUsage)
		}
		if contextActions.Compact != nil {
			bridge.SetHostAction("compact", func(ctx context.Context, opts *extension.CompactOptions) {
				extension.CallInitiated(ctx)
				contextActions.Compact(opts)
			})
		}
		if contextActions.GetSystemPrompt != nil {
			bridge.SetHostAction("getSystemPrompt", contextActions.GetSystemPrompt)
		}
		if contextActions.GetSystemPromptOptions != nil {
			bridge.SetHostAction("getSystemPromptOptions", func() extension.BuildSystemPromptOptions {
				if options := contextActions.GetSystemPromptOptions(); options != nil {
					return *options
				}
				return extension.NormalizeBuildSystemPromptOptions(extension.BuildSystemPromptOptions{Cwd: g.cwd})
			})
		}
		if contextActions.ExecuteTool != nil {
			bridge.SetHostAction("executeTool", contextActions.ExecuteTool)
		}
		if contextActions.GetCallableTools != nil {
			bridge.SetHostAction("getCallableTools", contextActions.GetCallableTools)
		}
		if g.session != nil {
			g.bindSessionReads(bridge)
		}
		bridge.HostStateChanged()
	}
}

// runBridgeSetModel is a setModel of a loaded extension, which names the model by its `provider/id` spec: the extension's function receives the model object of that name when the runner's model registry lists it.
func (g *GraphExtensionRunner) runBridgeSetModel(ctx context.Context, ids callbackIDs, spec string) (bool, error) {
	var model *ai.Model
	if registry := g.runner.GetModelRegistry(); registry != nil {
		for _, candidate := range registry.GetAll() {
			if candidate.ProviderMeta.ProviderID+"/"+candidate.ID == spec {
				model = candidate
				break
			}
		}
	}
	if model == nil {
		return false, fmt.Errorf("Model not found: %s", spec)
	}
	wire, err := wireModel(model)
	if err != nil {
		return false, err
	}
	return runDecoded[bool](g, ctx, ids, "setModel", wire)
}

// bindSessionReads gives the loaded extensions the log the runner was constructed over, as ctx.sessionManager.
func (g *GraphExtensionRunner) bindSessionReads(bridge *subprocess.UIBridge) {
	session := g.session
	bridge.SetHostAction("getSessionID", session.ID)
	bridge.SetHostAction("getSessionFile", session.Path)
	bridge.SetHostAction("getLeafID", func() string {
		if leaf := session.GetLeafID(); leaf != nil {
			return *leaf
		}
		return ""
	})
	bridge.SetHostAction("getEntriesPage", func(cursor, maxBytes int) ([]json.RawMessage, int, bool, string) {
		page, next, total := session.RawEntriesPage(cursor, maxBytes)
		if page == nil {
			page = make([]json.RawMessage, 0)
		}
		leafID := ""
		if leaf := session.GetLeafID(); leaf != nil {
			leafID = *leaf
		}
		return page, next, next < total, leafID
	})
	var info icodingagent.ExtensionSessionInfoMemo
	view := icodingagent.ExtensionSessionView{CWD: g.cwd, Session: session}
	bridge.SetHostAction("sessionRead", func(method string, args json.RawMessage) (any, error) {
		if method == "info" {
			return info.Encoded(view)
		}
		return icodingagent.ExtensionSessionRead(view, method, args)
	})
}
