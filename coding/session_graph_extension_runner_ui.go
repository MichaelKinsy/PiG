package coding

// The contexts and the UI context of the ExtensionRunner graph object. Pi's createContext, createToolContext and createCommandContext return plain objects whose getters read the runner at call time; the SDKs hold the same views and ask the runner for each member (`context`), so a read sees what the bound functions answer now and a stale runner throws its message. A member that takes functions of the caller (newSession's setup and withSession, compact's onComplete, executeTool's onUpdate) is served by the SDK from the functions it bound, after `assertActive`. `ui` calls the runner's UI context, which wraps the extension's own (Pi's wrapUIPromptContext) so a dialog reports ui_prompt_start and ui_prompt_end.

import (
	"context"
	"fmt"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

type graphContextSpec struct {
	Kind       string `json:"kind"`
	ToolCallID string `json:"toolCallId"`
}

// callContext is `context(spec, member, ...args)`.
func (g *GraphExtensionRunner) callContext(ctx context.Context, args icodingagent.GraphArgs) (any, error) {
	var spec graphContextSpec
	err := args.Unmarshal(0, func(raw json.RawMessage) error { return json.Unmarshal(raw, &spec) })
	if err != nil {
		return nil, err
	}
	member, err := args.String(1)
	if err != nil {
		return nil, err
	}
	var command *extension.CommandContext
	var tool *extension.ToolContext
	var base *extension.Context
	switch spec.Kind {
	case "tool":
		tool = g.runner.CreateToolContext(ctx, spec.ToolCallID)
		base = tool.Context
	case "command":
		command = g.runner.CreateCommandContext()
		base = command.Context
	case "context", "":
		base = g.runner.CreateCommandContext().Context
	default:
		return nil, fmt.Errorf("unknown context kind %q", spec.Kind)
	}
	switch member {
	case "assertActive":
		return nil, g.assertActive()
	case "cwd":
		return base.CWD()
	case "mode":
		return base.Mode()
	case "hasUI":
		return base.HasUI()
	case "model":
		model, err := base.Model()
		if err != nil || model == nil {
			return nil, err
		}
		return extension.ModelObject(model)
	case "scopedModels":
		scoped, err := base.ScopedModels()
		if err != nil {
			return nil, err
		}
		out := make([]map[string]any, 0, len(scoped))
		for _, entry := range scoped {
			model, err := extension.ModelObject(entry.Model)
			if err != nil {
				return nil, err
			}
			out = append(out, map[string]any{"model": model, "thinkingLevel": entry.ThinkingLevel})
		}
		return out, nil
	case "thinkingLevel":
		return base.ThinkingLevel()
	case "isIdle":
		return base.IsIdle()
	case "isProjectTrusted":
		return base.IsProjectTrusted()
	case "signal":
		signal, err := base.Signal()
		if err != nil || signal == nil {
			return nil, err
		}
		return map[string]any{"aborted": signal.Err() != nil}, nil
	case "abort":
		return nil, base.Abort()
	case "hasPendingMessages":
		return base.HasPendingMessages()
	case "shutdown":
		return nil, base.Shutdown()
	case "getContextUsage":
		return base.GetContextUsage()
	case "getSystemPrompt":
		return base.GetSystemPrompt()
	case "getSystemPromptOptions":
		if command == nil {
			return nil, fmt.Errorf("getSystemPromptOptions is a member of the command context")
		}
		if err := g.assertActive(); err != nil {
			return nil, err
		}
		// The bound function's own object, as Pi returns it: the typed options would add the members they default.
		g.mu.Lock()
		bound := g.boundContext
		g.mu.Unlock()
		if raw, ok, err := g.run(ctx, bound, "getSystemPromptOptions"); ok {
			return raw, err
		}
		return command.GetSystemPromptOptions()
	case "tools":
		if tool == nil {
			return nil, fmt.Errorf("tools is a member of the tool context")
		}
		tools, err := tool.Tools()
		return nonNilSlice(tools), err
	}
	return nil, fmt.Errorf("the context has no member %s", member)
}

// rawResultKey carries the pointer the UI context stores the extension function's own JSON answer in, so a call answers undefined and an empty string apart.
type rawResultKey struct{}

type uiRawResult struct {
	raw json.RawMessage
	set bool
}

func (r *uiRawResult) store(raw json.RawMessage) {
	if r != nil {
		r.raw, r.set = raw, true
	}
}

// graphUIContext is the ExtensionUIContext an extension passed to setUIContext: the functions it supplied run in its process, and a member it did not supply is Pi's no-op context's.
type graphUIContext struct {
	extension.UIContext
	g   *GraphExtensionRunner
	ids callbackIDs
}

func rawResultOf(ctx context.Context) *uiRawResult {
	result, _ := ctx.Value(rawResultKey{}).(*uiRawResult)
	return result
}

// ask runs the extension's function and keeps its answer for the call that is running.
func (u *graphUIContext) ask(ctx context.Context, name string, args ...any) (json.RawMessage, bool, error) {
	raw, ok, err := u.g.run(ctx, u.ids, name, args...)
	if ok && err == nil {
		rawResultOf(ctx).store(raw)
	}
	return raw, ok, err
}

func (u *graphUIContext) Select(ctx context.Context, title string, options []string, opts extension.ExtensionUIDialogOptions) (string, error) {
	raw, ok, err := u.ask(ctx, "select", title, options, opts)
	if !ok {
		return u.UIContext.Select(ctx, title, options, opts)
	}
	if err != nil {
		return "", err
	}
	return decodeJSON[string](raw)
}

func (u *graphUIContext) Confirm(ctx context.Context, title, message string, opts extension.ExtensionUIDialogOptions) (bool, error) {
	raw, ok, err := u.ask(ctx, "confirm", title, message, opts)
	if !ok {
		return u.UIContext.Confirm(ctx, title, message, opts)
	}
	if err != nil {
		return false, err
	}
	return decodeJSON[bool](raw)
}

func (u *graphUIContext) Input(ctx context.Context, title, placeholder string, opts extension.ExtensionUIDialogOptions) (string, error) {
	raw, ok, err := u.ask(ctx, "input", title, placeholder, opts)
	if !ok {
		return u.UIContext.Input(ctx, title, placeholder, opts)
	}
	if err != nil {
		return "", err
	}
	return decodeJSON[string](raw)
}

func (u *graphUIContext) Editor(ctx context.Context, title, prefill string) (string, error) {
	raw, ok, err := u.ask(ctx, "editor", title, prefill)
	if !ok {
		return u.UIContext.Editor(ctx, title, prefill)
	}
	if err != nil {
		return "", err
	}
	return decodeJSON[string](raw)
}

// notify runs a member of Pi's void UI functions; the runner's Notify and the setters have no error to return, so a failed function is reported to the runner's error listeners as Pi's thrown error would be to the handler that called it.
func (u *graphUIContext) void(name string, args ...any) {
	if _, _, err := u.g.run(callbackBackground(), u.ids, name, args...); err != nil {
		u.g.runner.EmitError(&extension.ExtensionError{Event: "ui." + name, Error: err.Error()})
	}
}

func (u *graphUIContext) Notify(message, kind string) {
	if u.ids["notify"] == "" {
		u.UIContext.Notify(message, kind)
		return
	}
	u.void("notify", message, kind)
}

func (u *graphUIContext) SetStatus(key, text string) {
	if u.ids["setStatus"] == "" {
		u.UIContext.SetStatus(key, text)
		return
	}
	u.void("setStatus", key, text)
}

func (u *graphUIContext) SetWorkingMessage(message string) {
	if u.ids["setWorkingMessage"] == "" {
		u.UIContext.SetWorkingMessage(message)
		return
	}
	u.void("setWorkingMessage", message)
}

func (u *graphUIContext) SetWorkingVisible(visible bool) {
	if u.ids["setWorkingVisible"] == "" {
		u.UIContext.SetWorkingVisible(visible)
		return
	}
	u.void("setWorkingVisible", visible)
}

func (u *graphUIContext) SetHiddenThinkingLabel(label string) {
	if u.ids["setHiddenThinkingLabel"] == "" {
		u.UIContext.SetHiddenThinkingLabel(label)
		return
	}
	u.void("setHiddenThinkingLabel", label)
}

func (u *graphUIContext) SetWidget(key string, content []string, opts *extension.ExtensionWidgetOptions) {
	if u.ids["setWidget"] == "" {
		u.UIContext.SetWidget(key, content, opts)
		return
	}
	u.void("setWidget", key, content, opts)
}

func (u *graphUIContext) SetTitle(title string) {
	if u.ids["setTitle"] == "" {
		u.UIContext.SetTitle(title)
		return
	}
	u.void("setTitle", title)
}

func (u *graphUIContext) PasteToEditor(text string) {
	if u.ids["pasteToEditor"] == "" {
		u.UIContext.PasteToEditor(text)
		return
	}
	u.void("pasteToEditor", text)
}

func (u *graphUIContext) SetEditorText(text string) {
	if u.ids["setEditorText"] == "" {
		u.UIContext.SetEditorText(text)
		return
	}
	u.void("setEditorText", text)
}

func (u *graphUIContext) GetEditorText() string {
	if u.ids["getEditorText"] == "" {
		return u.UIContext.GetEditorText()
	}
	text, err := runDecoded[string](u.g, callbackBackground(), u.ids, "getEditorText")
	if err != nil {
		u.g.runner.EmitError(&extension.ExtensionError{Event: "ui.getEditorText", Error: err.Error()})
	}
	return text
}

func (u *graphUIContext) GetToolsExpanded() bool {
	if u.ids["getToolsExpanded"] == "" {
		return u.UIContext.GetToolsExpanded()
	}
	expanded, err := runDecoded[bool](u.g, callbackBackground(), u.ids, "getToolsExpanded")
	if err != nil {
		u.g.runner.EmitError(&extension.ExtensionError{Event: "ui.getToolsExpanded", Error: err.Error()})
	}
	return expanded
}

func (u *graphUIContext) SetToolsExpanded(expanded bool) {
	if u.ids["setToolsExpanded"] == "" {
		u.UIContext.SetToolsExpanded(expanded)
		return
	}
	u.void("setToolsExpanded", expanded)
}

// setUIContext is `setUIContext(uiContext?, mode = "print")`.
func (g *GraphExtensionRunner) setUIContext(args icodingagent.GraphArgs) error {
	var ids callbackIDs
	err := args.Unmarshal(0, func(raw json.RawMessage) error { return json.Unmarshal(raw, &ids) })
	if err != nil {
		return err
	}
	mode, err := args.String(1)
	if err != nil {
		return err
	}
	if mode == "" {
		mode = string(extension.ModePrint)
	}
	var ui extension.UIContext
	if len(ids) != 0 || args.Has(0) {
		ui = &graphUIContext{UIContext: extension.NoopUIContext, g: g, ids: ids}
	}
	g.runner.SetUIContext(ui, extension.ExtensionMode(mode))
	for _, bridge := range g.bridges {
		bridge.SetUIContext(g.runner.GetUIContext())
	}
	for _, host := range g.hosts {
		host.SetMode(mode)
	}
	return nil
}

// callUI is `ui(member, ...args)`: a member of the runner's UI context, which the extension reaches through ctx.ui.
func (g *GraphExtensionRunner) callUI(ctx context.Context, args icodingagent.GraphArgs) (any, error) {
	if err := g.assertActive(); err != nil {
		return nil, err
	}
	member, err := args.String(0)
	if err != nil {
		return nil, err
	}
	result := &uiRawResult{}
	ctx = context.WithValue(ctx, rawResultKey{}, result)
	ui := g.runner.GetUIContext()
	answer := func(value any, err error) (any, error) {
		if err != nil {
			return nil, err
		}
		if result.set {
			return result.raw, nil
		}
		return value, nil
	}
	// A dialog the extension's function did not answer (a no-op UI, Pi's undefined) answers the empty string; it is undefined here.
	answerText := func(value string, err error) (any, error) {
		if err == nil && !result.set && value == "" {
			return nil, nil
		}
		return answer(value, err)
	}
	switch member {
	case "select":
		title, _ := args.String(1)
		var options []string
		err := args.Unmarshal(2, func(raw json.RawMessage) error { return json.Unmarshal(raw, &options) })
		if err != nil {
			return nil, err
		}
		var opts extension.ExtensionUIDialogOptions
		err = args.Unmarshal(3, func(raw json.RawMessage) error { return json.Unmarshal(raw, &opts) })
		if err != nil {
			return nil, err
		}
		return answerText(ui.Select(ctx, title, options, opts))
	case "confirm":
		title, _ := args.String(1)
		message, _ := args.String(2)
		var opts extension.ExtensionUIDialogOptions
		err := args.Unmarshal(3, func(raw json.RawMessage) error { return json.Unmarshal(raw, &opts) })
		if err != nil {
			return nil, err
		}
		return answer(ui.Confirm(ctx, title, message, opts))
	case "input":
		title, _ := args.String(1)
		placeholder, _ := args.String(2)
		var opts extension.ExtensionUIDialogOptions
		err := args.Unmarshal(3, func(raw json.RawMessage) error { return json.Unmarshal(raw, &opts) })
		if err != nil {
			return nil, err
		}
		return answerText(ui.Input(ctx, title, placeholder, opts))
	case "editor":
		title, _ := args.String(1)
		prefill, _ := args.String(2)
		return answerText(ui.Editor(ctx, title, prefill))
	case "notify":
		message, _ := args.String(1)
		kind, _ := args.String(2)
		ui.Notify(message, kind)
	case "setStatus":
		key, _ := args.String(1)
		text, _ := args.String(2)
		ui.SetStatus(key, text)
	case "setWorkingMessage":
		message, _ := args.String(1)
		ui.SetWorkingMessage(message)
	case "setWorkingVisible":
		visible, _ := args.Bool(1)
		ui.SetWorkingVisible(visible)
	case "setHiddenThinkingLabel":
		label, _ := args.String(1)
		ui.SetHiddenThinkingLabel(label)
	case "setWidget":
		key, _ := args.String(1)
		var content []string
		err := args.Unmarshal(2, func(raw json.RawMessage) error { return json.Unmarshal(raw, &content) })
		if err != nil {
			return nil, err
		}
		var opts *extension.ExtensionWidgetOptions
		err = args.Unmarshal(3, func(raw json.RawMessage) error { return json.Unmarshal(raw, &opts) })
		if err != nil {
			return nil, err
		}
		ui.SetWidget(key, content, opts)
	case "setTitle":
		title, _ := args.String(1)
		ui.SetTitle(title)
	case "pasteToEditor":
		text, _ := args.String(1)
		ui.PasteToEditor(text)
	case "setEditorText":
		text, _ := args.String(1)
		ui.SetEditorText(text)
	case "getEditorText":
		return ui.GetEditorText(), nil
	case "getToolsExpanded":
		return ui.GetToolsExpanded(), nil
	case "setToolsExpanded":
		expanded, _ := args.Bool(1)
		ui.SetToolsExpanded(expanded)
	default:
		return nil, fmt.Errorf("the UI context has no member %s", member)
	}
	return nil, nil
}
