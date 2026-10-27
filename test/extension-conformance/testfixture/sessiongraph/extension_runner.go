package sessiongraph

import (
	"context"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// errorOf is Pi's failure(): "no error", or the error's message.
func errorOf(err error) string {
	if err != nil {
		return "error: " + err.Error()
	}
	return "no error"
}

// runnerLog is the calls the script's bound actions received, in order. The host calls them from its own goroutines.
type runnerLog struct {
	mu      sync.Mutex
	entries []any
}

func (l *runnerLog) record(name string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, append([]any{name}, args...))
}

func (l *runnerLog) count(name, customType string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	count := 0
	for _, entry := range l.entries {
		call := entry.([]any)
		if call[0] == name && len(call) > 1 && call[1] == customType {
			count++
		}
	}
	return count
}

func (l *runnerLog) snapshot() []any {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]any{}, l.entries...)
}

// extensionRunnerScript runs createExtensionRuntime and ExtensionRunner over two probe extensions, as testdata/extension-runner.mjs does with Pi's own classes; the report is Pi's own run's.
func extensionRunnerScript(ctx sdk.Context, args map[string]any) map[string]any {
	str := func(key string) string { return args[key].(string) }
	cwd, agentDir := str("cwd"), str("agentDir")
	r := map[string]any{}

	runtimeView := func(runtime *sdk.ExtensionRuntime) map[string]any {
		providers := ok(runtime.PendingProviderRegistrations())
		native := ok(runtime.PendingNativeProviderRegistrations())
		virtual := ok(runtime.PendingVirtualModelRegistrations())
		servers := ok(runtime.McpServers())
		return map[string]any{"flagValues": ok(runtime.GetFlagValues()), "providers": len(providers), "nativeProviders": len(native), "virtualModels": len(virtual), "mcpServers": len(servers)}
	}
	fresh := ok(sdk.CreateExtensionRuntime(ctx))
	runtimeReport := map[string]any{"initial": runtimeView(fresh), "active": errorOf(fresh.AssertActive())}
	_ = fresh.SetFlagValue("x", true)
	_ = fresh.SetFlagValue("a", "b")
	runtimeReport["afterSet"] = runtimeView(fresh)
	first, second := "runtime is stale", "a second message is ignored"
	_ = fresh.Invalidate(&first)
	_ = fresh.Invalidate(&second)
	runtimeReport["stale"] = errorOf(fresh.AssertActive())
	r["runtime"] = runtimeReport

	models, store := str("mrModels"), str("mrStore")
	rt := ok(sdk.ModelRuntimeCreate(ctx, sdk.ModelRuntimeCreateOptions{AuthPath: str("mrAuth"), ModelsPath: &models, ModelsStorePath: store}))
	settings := ok(sdk.SettingsManagerInMemory(ctx, map[string]any{
		"compaction": map[string]any{"enabled": false}, "retry": map[string]any{"enabled": false}, "defaultProvider": "mr-local", "defaultModel": "m1",
	}, nil))
	loaderRuntime := ok(sdk.CreateExtensionRuntime(ctx))
	loader := ok(sdk.NewDefaultResourceLoader(ctx, sdk.DefaultResourceLoaderOptions{
		Cwd: cwd, AgentDir: agentDir, SettingsManager: &settings, Runtime: loaderRuntime, NoExtensions: true, NoSkills: true, NoPromptTemplates: true, NoThemes: true, NoContextFiles: true,
		AdditionalExtensionPaths: []string{str("runnerProbeA"), str("runnerProbeB")},
	}))
	if err := loader.Reload(nil); err != nil {
		panic(err)
	}
	loaded := ok(loader.GetExtensions())
	paths, tools, sources := []string{}, [][]string{}, []string{}
	for _, ext := range loaded.Extensions {
		paths = append(paths, ext.Path)
		tools = append(tools, ext.Tools)
		sources = append(sources, ext.SourceInfo.Source)
	}
	loadErrors := []any{}
	for _, loadErr := range loaded.Errors {
		loadErrors = append(loadErrors, map[string]any{"path": loadErr.Path, "error": loadErr.Error})
	}
	r["loaded"] = map[string]any{"paths": paths, "tools": tools, "errors": loadErrors, "sources": sources, "runtime": runtimeView(loaded.Runtime)}

	manager := ok(sdk.SessionManagerInMemory(ctx, cwd, nil, nil))
	runner := ok(sdk.NewExtensionRunner(ctx, loaded.Extensions, loaded.Runtime, cwd, &manager, rt))

	// The registries, before anything is bound.
	keybindings := map[string][]string{"app.interrupt": {"ctrl+c"}, "app.other": {"ctrl+k"}, "app.exit": {"ctrl+d"}}
	noop := runner.GetUIContext()
	toolView := func(tool sdk.RegisteredTool) map[string]any {
		return map[string]any{"name": tool.Definition.Name, "description": tool.Definition.Description, "scope": tool.SourceInfo.Scope, "origin": tool.SourceInfo.Origin, "path": tool.SourceInfo.Path}
	}
	commandView := func(command sdk.ResolvedCommand) map[string]any {
		var description any
		if command.Description != "" {
			description = command.Description
		}
		return map[string]any{"name": command.Name, "invocationName": command.InvocationName, "description": description, "path": command.SourceInfo.Path}
	}
	registered := ok(runner.GetAllRegisteredTools())
	toolViews := []any{}
	for _, tool := range registered {
		toolViews = append(toolViews, toolView(tool))
	}
	shared := ok(runner.GetToolDefinition("shared"))
	missing := ok(runner.GetToolDefinition("nope"))
	shortcuts := []any{}
	for _, entry := range ok(runner.GetShortcuts(keybindings)) {
		shortcuts = append(shortcuts, map[string]any{"key": entry.Key, "description": entry.Shortcut.Description, "extensionPath": entry.Shortcut.ExtensionPath})
	}
	commands := []any{}
	for _, command := range ok(runner.GetRegisteredCommands()) {
		commands = append(commands, commandView(command))
	}
	invocation := func(name string) any {
		if command := ok(runner.GetCommand(name)); command != nil {
			return command.InvocationName
		}
		return nil
	}
	var twinTwo any
	if command := ok(runner.GetCommand("twin:2")); command != nil {
		twinTwo = command.Description
	}
	typeOf := func(found bool) string {
		if found {
			return "function"
		}
		return "undefined"
	}
	rendererFound := func(renderer *sdk.ExtensionRenderer) string { return typeOf(renderer != nil) }
	toolRenderers := func(toolName string, base func() *sdk.ResolvedToolRenderers) any {
		found := ok(runner.ResolveToolRenderers(toolName, base))
		if found == nil {
			return nil
		}
		var shell any
		if found.RenderShell != "" {
			shell = found.RenderShell
		}
		return map[string]any{"renderShell": shell, "renderCall": typeOf(found.RenderCall), "renderResult": typeOf(found.RenderResult)}
	}
	// An extension process answers a tool renderer resolution on a later turn (D89): the first call answers what the base does, so ask until the answer is the extension's.
	resolvedAlpha := func() any {
		var answer any
		for range 100 {
			if answer = toolRenderers("alpha", func() *sdk.ResolvedToolRenderers { return nil }); answer != nil {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		return answer
	}
	handlers := func(eventType string) bool { return ok(runner.HasHandlers(eventType)) }
	noopSelect, noopInput := ok(noop.Select("t", []string{"a"}, nil)), ok(noop.Input("t", nil, nil))
	noopEditor := ok(noop.GetEditorText())
	var noopSelectValue, noopInputValue any
	if noopSelect != nil {
		noopSelectValue = *noopSelect
	}
	if noopInput != nil {
		noopInputValue = *noopInput
	}
	contextOf := runner.CreateContext()
	registry := ok(contextOf.ModelRegistry())
	sessionManager := ok(contextOf.SessionManager())
	cwdValue, modeValue := ok(contextOf.Cwd()), ok(contextOf.Mode())
	_, activeErr := runner.GetActiveTools()
	commandBefore := runner.CreateCommandContext()
	if err := commandBefore.WaitForIdle(); err != nil {
		panic(err)
	}
	newBefore := ok(commandBefore.NewSession(nil))
	if err := commandBefore.Reload(); err != nil {
		panic(err)
	}
	nestedBefore := ok(runner.CreateToolContext("call-0", nil).ExecuteTool("alpha", map[string]any{}, sdk.ExecuteToolOptions{}))
	reads := map[string]any{
		"extensionPaths": ok(runner.GetExtensionPaths()),
		"tools":          toolViews,
		"toolDefinition": shared.Description,
		"missingTool":    missing,
		"flags":          ok(runner.GetFlags()),
		"flagValues":     ok(runner.GetFlagValues()),
		"shortcuts":      shortcuts, "shortcutDiagnostics": ok(runner.GetShortcutDiagnostics()),
		"commands": commands, "commandDiagnostics": ok(runner.GetCommandDiagnostics()),
		"commandByName": map[string]any{"hello": invocation("hello"), "twin": invocation("twin"), "twin:2": twinTwo, "nope": invocation("nope")},
		"hasHandlers":   map[string]any{"tool_call": handlers("tool_call"), "session_shutdown": handlers("session_shutdown")},
		"renderers": map[string]any{
			"message": rendererFound(ok(runner.GetMessageRenderer("probe-message"))), "missingMessage": rendererFound(ok(runner.GetMessageRenderer("nope"))),
			"entry": rendererFound(ok(runner.GetEntryRenderer("probe-entry"))), "missingEntry": rendererFound(ok(runner.GetEntryRenderer("nope"))),
			"markdownTransformers": len(ok(runner.GetMarkdownTransformers())),
		},
		"toolRenderers": map[string]any{
			"alpha": resolvedAlpha(),
			"other": toolRenderers("other", func() *sdk.ResolvedToolRenderers { return &sdk.ResolvedToolRenderers{RenderResult: true} }),
			"none":  toolRenderers("other", func() *sdk.ResolvedToolRenderers { return nil }),
		},
		"hasUI":                    ok(runner.HasUI()),
		"noUI":                     map[string]any{"select": noopSelectValue, "confirm": ok(noop.Confirm("t", "m", nil)), "input": noopInputValue, "editorText": noopEditor},
		"registryIsSame":           runner.GetModelRegistry() == rt,
		"activeTools":              errorOf(activeErr),
		"mode":                     modeValue,
		"cwd":                      cwdValue,
		"sessionManagerIsSame":     sessionManager == &manager,
		"modelRegistryIsSame":      registry == rt,
		"commandContextBeforeBind": map[string]any{"newSession": newBefore},
		"toolContextBeforeBind":    nestedBefore.Result.Content[0].Text,
	}
	_ = runner.SetFlagValue("probe-flag", "changed")
	reads["flagValuesAfterSet"] = ok(runner.GetFlagValues())
	_ = runner.SetFlagValue("probe-bool", true)
	reads["flagValuesAfterBool"] = ok(runner.GetFlagValues())
	r["reads"] = reads

	// Bound actions: every call an extension makes reaches one of these, and is logged.
	log := &runnerLog{}
	mrModel := ok(rt.GetModel("mr-local", "m1"))
	sourceInfo := sdk.SourceInfo{Path: "p", Source: "local", Scope: "temporary", Origin: "top-level"}
	actions := sdk.ExtensionActions{
		SendMessage: func(message sdk.CustomMessage, options *sdk.SendMessageOptions) error {
			log.record("sendMessage", map[string]any{"customType": message.CustomType, "content": message.Content, "display": message.Display}, options)
			return nil
		},
		SendUserMessage: func(content any, options *sdk.SendUserMessageOptions) error {
			log.record("sendUserMessage", content, options)
			return nil
		},
		AppendEntry:    func(customType string, data any) error { log.record("appendEntry", customType, data); return nil },
		SetSessionName: func(name string) error { log.record("setSessionName", name); return nil },
		GetSessionName: func() string { return "session-name" },
		SetLabel:       func(entryID string, label *string) error { log.record("setLabel", entryID, label); return nil },
		GetActiveTools: func() []string { return []string{"alpha", "beta"} },
		GetAllTools: func() []sdk.ToolInfo {
			return []sdk.ToolInfo{{Name: "alpha", Description: "Alpha tool", Parameters: []byte(`{"type":"object"}`), SourceInfo: sourceInfo}}
		},
		GetSettings:    func() sdk.Settings { return sdk.Settings{"theme": "dark"} },
		SetActiveTools: func(names []string) error { log.record("setActiveTools", names); return nil },
		RefreshTools:   func() error { log.record("refreshTools"); return nil },
		GetCommands: func() []sdk.CommandInfo {
			return []sdk.CommandInfo{{Name: "hello", Description: "d", Source: "extension", SourceInfo: sourceInfo}}
		},
		SetModel: func(model map[string]any) (bool, error) {
			log.record("setModel", model["id"])
			return true, nil
		},
		GetThinkingLevel: func() string { return "medium" },
		SetThinkingLevel: func(level string) { log.record("setThinkingLevel", level) },
	}
	contextActions := sdk.ExtensionContextActions{
		GetModel:           func() map[string]any { return mrModel },
		GetScopedModels:    func() []sdk.ScopedModel { return []sdk.ScopedModel{{Model: mrModel, ThinkingLevel: "low"}} },
		IsIdle:             func() bool { return false },
		IsProjectTrusted:   func() bool { return true },
		GetSignal:          func() context.Context { return nil },
		Abort:              func() { log.record("abort") },
		HasPendingMessages: func() bool { return true },
		Shutdown:           func() { log.record("shutdown") },
		GetContextUsage: func() *sdk.ContextUsage {
			tokens, percent := 10, 10.0
			return &sdk.ContextUsage{Tokens: &tokens, ContextWindow: 100, Percent: &percent}
		},
		Compact:                func(options *sdk.CompactOptions) { log.record("compact", options.CustomInstructions) },
		GetSystemPrompt:        func() string { return "bound system prompt" },
		GetSystemPromptOptions: func() *sdk.SystemPromptOptions { return &sdk.SystemPromptOptions{Cwd: cwd, CustomPrompt: "custom"} },
		ExecuteTool: func(callerID, name string, params any, _ sdk.ExecuteToolOptions) (sdk.AgentToolCallOutcome, error) {
			arguments, _ := params.(map[string]any)
			return sdk.AgentToolCallOutcome{
				ToolCall: sdk.ToolCall{Type: "toolCall", ID: callerID + "/1", Name: name, Arguments: arguments},
				Result:   sdk.AgentToolResult{Content: []sdk.ToolResultContent{{Type: "text", Text: "ran " + name}}, Details: map[string]any{}},
			}, nil
		},
		GetCallableTools: func() []sdk.AgentTool {
			return []sdk.AgentTool{{Name: "alpha", Label: "Alpha", Description: "Alpha tool", Parameters: []byte(`{"type":"object"}`)}}
		},
	}
	if err := runner.BindCore(actions, contextActions, nil); err != nil {
		panic(err)
	}
	r["bound"] = map[string]any{"activeTools": ok(runner.GetActiveTools()), "errors": []any{}}
	var errorsMu sync.Mutex
	reported := []any{}
	off := ok(runner.OnError(func(report sdk.ExtensionError) {
		errorsMu.Lock()
		defer errorsMu.Unlock()
		reported = append(reported, map[string]any{"extensionPath": report.ExtensionPath, "event": report.Event, "error": report.Error})
	}))

	if err := runner.ReportUnhandledMcpServers(); err != nil {
		panic(err)
	}
	// The emit family, each through two handlers that chain.
	emits := map[string]any{}
	ok(runner.Emit(map[string]any{"type": "session_start", "reason": "startup"}))
	emits["sessionBeforeSwitch"] = ok(runner.Emit(map[string]any{"type": "session_before_switch", "reason": "new"}))
	ok(runner.Emit(map[string]any{"type": "agent_start"}))
	if value := ok(runner.EmitToolCall(map[string]any{"type": "tool_call", "toolCallId": "t1", "toolName": "bash", "input": map[string]any{"command": "ls"}})); value != nil {
		emits["toolCall"] = value
	}
	if value := ok(runner.EmitToolCall(map[string]any{"type": "tool_call", "toolCallId": "t2", "toolName": "bash", "input": map[string]any{"command": "forbidden"}})); value != nil {
		emits["toolCallBlocked"] = value
	}
	if value := ok(runner.EmitToolResult(map[string]any{"type": "tool_result", "toolCallId": "t1", "toolName": "bash", "input": map[string]any{"command": "ls"}, "content": []any{map[string]any{"type": "text", "text": "out"}}, "isError": false})); value != nil {
		emits["toolResult"] = value
	}
	emits["inputTransform"] = ok(runner.EmitInput("t:x", nil, "interactive", nil))
	emits["inputHandled"] = ok(runner.EmitInput("handled-me", nil, "interactive", nil))
	emits["inputContinue"] = ok(runner.EmitInput("plain", nil, "rpc", nil))
	emits["context"] = ok(runner.EmitContext([]map[string]any{{"role": "user", "content": "keep", "timestamp": 1}, {"role": "user", "content": "drop-me", "timestamp": 2}}))
	started := ok(runner.EmitBeforeAgentStart(map[string]any{"messageType": "user", "prompt": "do it"}, &sdk.SystemPromptOptions{Cwd: cwd, CustomPrompt: "base"}))
	options := started["systemPromptOptions"].(map[string]any)
	forced := options["forceSystemPrompt"].(string)
	emits["beforeAgentStart"] = map[string]any{
		"messages": started["messages"], "forced": map[string]any{"startsWithBase": strings.HasPrefix(forced, "base"), "endsWithA": strings.HasSuffix(forced, "[A]")}, "customPrompt": options["customPrompt"],
	}
	if value := ok(runner.EmitMessageEnd(map[string]any{"type": "message_end", "message": map[string]any{"role": "user", "content": "hi", "timestamp": 1}})); value != nil {
		emits["messageEnd"] = value
	}
	emits["providerRequest"] = ok(runner.EmitBeforeProviderRequest(map[string]any{"model": "m"}))
	deleted, kept := "d", "k"
	emits["providerHeaders"] = ok(runner.EmitBeforeProviderHeaders(map[string]*string{"x-del": &deleted, "x-keep": &kept}))
	if value := ok(runner.EmitUserBash(map[string]any{"type": "user_bash", "command": "ls", "excludeFromContext": false, "cwd": cwd})); value != nil {
		emits["userBash"] = value
	}
	emits["resourcesDiscover"] = ok(runner.EmitResourcesDiscover(cwd, "startup"))
	emits["cacheWarming"] = ok(runner.EmitCacheWarmingDecision(map[string]any{"type": "cache_warming_decision", "warmCost": 1, "missCost": 2, "continuationProbability": 0.5, "action": "warm"}))
	built := []any{}
	emits["boundary"] = ok(runner.EmitBoundary(map[string]any{"type": "turn_end", "turnIndex": 0, "message": map[string]any{"role": "user", "content": "x", "timestamp": 1}, "toolResults": []any{}, "messageEntryId": "m1", "toolResultEntryIds": []any{}, "outcome": "completed"}, func(entries []map[string]any) (map[string]any, error) {
		built = append(built, len(entries))
		return map[string]any{"contextEntries": []any{}, "contextMessages": []any{}, "llmMessages": []any{}, "pendingMessages": []any{}, "canContinue": true}, nil
	}))
	emits["boundaryBuilt"] = built
	if err := runner.EmitError(sdk.ExtensionError{ExtensionPath: "/x/ext.mjs", Event: "manual", Error: "reported by hand"}); err != nil {
		panic(err)
	}
	r["emits"] = emits
	errorsMu.Lock()
	r["errors"] = append([]any{}, reported...)
	errorsMu.Unlock()

	// The contexts, which read the bound functions at call time.
	live := runner.CreateContext()
	var signal any
	if value := ok(live.Signal()); value != nil {
		signal = map[string]any{"aborted": value.Err() != nil}
	}
	model := ok(live.Model())
	scopedModels := []any{}
	for _, scoped := range ok(live.ScopedModels()) {
		scopedModels = append(scopedModels, []any{scoped.Model["id"], scoped.ThinkingLevel})
	}
	contexts := map[string]any{
		"mode": ok(live.Mode()), "hasUI": ok(live.HasUI()), "cwd": ok(live.Cwd()), "model": model["id"], "scopedModels": scopedModels, "thinkingLevel": ok(live.ThinkingLevel()),
		"isIdle": ok(live.IsIdle()), "isProjectTrusted": ok(live.IsProjectTrusted()), "signal": signal, "hasPendingMessages": ok(live.HasPendingMessages()), "contextUsage": ok(live.GetContextUsage()), "systemPrompt": ok(live.GetSystemPrompt()),
	}
	if err := live.Abort(); err != nil {
		panic(err)
	}
	if err := live.Shutdown(); err != nil {
		panic(err)
	}
	if err := live.Compact(&sdk.CompactOptions{CustomInstructions: "short"}); err != nil {
		panic(err)
	}
	if err := runner.Shutdown(); err != nil {
		panic(err)
	}
	command := runner.CreateCommandContext()
	if err := runner.BindCommandContext(&sdk.ExtensionCommandContextActions{
		WaitForIdle: func() error { log.record("waitForIdle"); return nil },
		NewSession: func(options *sdk.CommandNewSessionOptions) (sdk.CancelledResult, error) {
			log.record("newSession", options.ParentSession)
			return sdk.CancelledResult{}, nil
		},
		Fork: func(entryID string, options *sdk.ForkOptions) (sdk.CancelledResult, error) {
			log.record("fork", entryID, options.Position)
			return sdk.CancelledResult{Cancelled: true}, nil
		},
		NavigateTree: func(target string, options *sdk.NavigateTreeOptions) (sdk.CancelledResult, error) {
			log.record("navigateTree", target, options.Label)
			return sdk.CancelledResult{}, nil
		},
		SwitchSession: func(path string, _ *sdk.SwitchSessionOptions) (sdk.CancelledResult, error) {
			log.record("switchSession", path)
			return sdk.CancelledResult{}, nil
		},
		Reload: func() error { log.record("reload"); return nil },
	}); err != nil {
		panic(err)
	}
	// In Pi's order: the calls log in the order they are made.
	commandReport := map[string]any{"systemPromptOptions": ok(command.GetSystemPromptOptions())}
	if err := command.WaitForIdle(); err != nil {
		panic(err)
	}
	commandReport["waitForIdle"] = nil
	commandReport["newSession"] = ok(command.NewSession(&sdk.CommandNewSessionOptions{ParentSession: "parent.jsonl"}))
	commandReport["fork"] = ok(command.Fork("e1", &sdk.ForkOptions{Position: "at"}))
	commandReport["navigateTree"] = ok(command.NavigateTree("e2", &sdk.NavigateTreeOptions{Label: "l"}))
	commandReport["switchSession"] = ok(command.SwitchSession("other.jsonl", nil))
	if err := command.Reload(); err != nil {
		panic(err)
	}
	commandReport["reload"] = nil
	contexts["command"] = commandReport
	toolContext := runner.CreateToolContext("call-1", nil)
	callable := []any{}
	for _, tool := range ok(toolContext.Tools()) {
		callable = append(callable, tool.Name)
	}
	contexts["tool"] = map[string]any{"tools": callable, "executeTool": ok(toolContext.ExecuteTool("alpha", map[string]any{"text": "x"}, sdk.ExecuteToolOptions{}))}
	r["contexts"] = contexts

	// The UI context: the dialogs report ui_prompt_start and ui_prompt_end to the extensions.
	var uiMu sync.Mutex
	uiLog := []any{}
	note := func(call ...any) {
		uiMu.Lock()
		defer uiMu.Unlock()
		uiLog = append(uiLog, call)
	}
	orNil := func(value *string) any {
		if value == nil {
			return nil
		}
		return *value
	}
	ui := &sdk.ExtensionUIContext{
		Select: func(title string, options []string, _ *sdk.ExtensionUIDialogOptions) (*string, error) {
			note("select", title, options)
			return &options[1], nil
		},
		Confirm: func(title, message string, _ *sdk.ExtensionUIDialogOptions) (bool, error) {
			note("confirm", title, message)
			return true, nil
		},
		Input: func(title string, placeholder *string, _ *sdk.ExtensionUIDialogOptions) (*string, error) {
			note("input", title, orNil(placeholder))
			typed := "typed"
			return &typed, nil
		},
		Editor: func(title string, prefill *string) (*string, error) {
			note("editor", title, orNil(prefill))
			edited := *prefill + "!"
			return &edited, nil
		},
		Notify:                 func(message string, kind *string) { note("notify", message, orNil(kind)) },
		SetStatus:              func(key string, text *string) { note("setStatus", key, orNil(text)) },
		SetWorkingMessage:      func(message *string) { note("setWorkingMessage", orNil(message)) },
		SetWorkingVisible:      func(visible bool) { note("setWorkingVisible", visible) },
		SetHiddenThinkingLabel: func(label *string) { note("setHiddenThinkingLabel", orNil(label)) },
		SetWidget: func(key string, content []string, options *sdk.ExtensionWidgetOptions) {
			var placement any
			if options != nil {
				placement = options.Placement
			}
			note("setWidget", key, content, placement)
		},
		SetTitle:         func(title string) { note("setTitle", title) },
		PasteToEditor:    func(text string) { note("pasteToEditor", text) },
		SetEditorText:    func(text string) { note("setEditorText", text) },
		GetEditorText:    func() string { return "editor text" },
		GetToolsExpanded: func() bool { return true },
		SetToolsExpanded: func(expanded bool) { note("setToolsExpanded", expanded) },
	}
	if err := runner.SetUIContext(ui, "tui"); err != nil {
		panic(err)
	}
	liveUI := runner.GetUIContext()
	dialogs := map[string]any{
		"select": orNil(ok(liveUI.Select("Pick", []string{"x", "y"}, nil))), "confirm": ok(liveUI.Confirm("Sure?", "really", nil)),
		"input": orNil(ok(liveUI.Input("Name", new("placeholder"), nil))), "editor": orNil(ok(liveUI.Editor("Edit", new("pre")))),
	}
	for _, step := range []error{
		liveUI.Notify("hello", new("info")), liveUI.SetStatus("k", new("v")), liveUI.SetWorkingMessage(new("working")), liveUI.SetWorkingVisible(false), liveUI.SetHiddenThinkingLabel(new("hidden")),
		liveUI.SetWidget("w", []string{"line"}, &sdk.ExtensionWidgetOptions{Placement: "belowEditor"}), liveUI.SetTitle("title"), liveUI.PasteToEditor("pasted"), liveUI.SetEditorText("set"), liveUI.SetToolsExpanded(false),
	} {
		if step != nil {
			panic(step)
		}
	}
	readsUI := map[string]any{"editorText": ok(liveUI.GetEditorText()), "toolsExpanded": ok(liveUI.GetToolsExpanded())}
	// The prompt events are delivered on a later turn: wait for both of each dialog.
	for i := 0; i < 100 && log.count("appendEntry", "a_ui") < 8; i++ {
		time.Sleep(50 * time.Millisecond)
	}
	after := runner.CreateContext()
	uiMu.Lock()
	r["ui"] = map[string]any{"mode": ok(after.Mode()), "hasUI": ok(runner.HasUI()), "contextHasUI": ok(after.HasUI()), "dialogs": dialogs, "reads": readsUI, "calls": append([]any{}, uiLog...)}
	uiMu.Unlock()
	if err := runner.SetUIContext(nil, ""); err != nil {
		panic(err)
	}
	cleared := runner.CreateContext()
	r["ui"].(map[string]any)["afterClear"] = map[string]any{"hasUI": ok(runner.HasUI()), "mode": ok(cleared.Mode())}

	// Listeners and a stale runner.
	off()
	if err := runner.EmitError(sdk.ExtensionError{ExtensionPath: "/x/ext.mjs", Event: "after-off", Error: "not delivered"}); err != nil {
		panic(err)
	}
	errorsMu.Lock()
	r["errors"] = append([]any{}, reported...)
	errorsMu.Unlock()
	stale, again := "runner is stale", "a second message is ignored"
	_ = runner.Invalidate(&stale)
	_ = runner.Invalidate(&again)
	staleContext := runner.CreateContext()
	_, cwdErr := staleContext.Cwd()
	_, idleErr := staleContext.IsIdle()
	_, activeErr = runner.GetActiveTools()
	waitErr := runner.CreateCommandContext().WaitForIdle()
	_, toolErr := runner.CreateToolContext("c", nil).ExecuteTool("alpha", map[string]any{}, sdk.ExecuteToolOptions{})
	_, uiErr := staleContext.UI()
	_, emitErr := runner.Emit(map[string]any{"type": "agent_start"})
	r["stale"] = map[string]any{
		"cwd": errorOf(cwdErr), "isIdle": errorOf(idleErr), "activeTools": errorOf(activeErr), "commandContext": errorOf(waitErr), "toolContext": errorOf(toolErr), "ui": errorOf(uiErr), "emit": errorOf(emitErr),
	}
	r["log"] = log.snapshot()
	return r
}
