// pig-specific: serves Pi's ExtensionRunner (packages/coding-agent/src/core/extensions/runner.ts) to the Go, Python and Rust extension SDKs as a host-owned object an extension names by handle. The object adapts the runtime's own *inproc.Runner, which is held to Pi's runner tests, over the extensions a DefaultResourceLoader of the same connection loaded: they stay in their subprocesses, and the runner dispatches to them as the Session's runner does. The functions Pi's bindCore, bindCommandContext and setUIContext take stay in the extension that built the runner and are named to the host as session graph callbacks. The Node runtime serves Pi's own class.

package coding

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"sync"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// GraphExtensionRunner is an ExtensionRunner an extension constructed.
type GraphExtensionRunner struct {
	runner     *inproc.Runner
	runtime    *extension.ExtensionRuntime
	scope      subprocess.SessionGraphScope
	extensions []extension.Extension
	// bridges are the UI bridges of the loaders whose extensions the runner dispatches to: the state their extensions' contexts read comes from the functions bindCore received.
	bridges []*subprocess.UIBridge
	hosts   []*subprocess.Host
	session *icodingagent.Session
	cwd     string

	// holds end the runner's hold of the objects it uses: the loaders whose extensions it dispatches to, which stop when their last holder lets go, and the log and registry its contexts read.
	holds []func()

	mu sync.Mutex
	// boundContext is the context functions bindCore received, by name.
	boundContext callbackIDs
	nextError    uint64
	errorOffs    map[string]func()
}

var _ subprocess.SessionGraphObject = (*GraphExtensionRunner)(nil)

// graphExtensionRunnerOptions is Pi's constructor (extensions, runtime, cwd, sessionManager, modelRegistry) as it crosses the wire: an extension is named by the loader that loaded it and its resolved path, an object is the handle of the object.
type graphExtensionRunnerOptions struct {
	Extensions []struct {
		Loader       string `json:"loader"`
		ResolvedPath string `json:"resolvedPath"`
	} `json:"extensions"`
	Runtime        string `json:"runtime"`
	Cwd            string `json:"cwd"`
	SessionManager string `json:"sessionManager"`
	ModelRegistry  string `json:"modelRegistry"`
}

func newGraphExtensionRunner(scope subprocess.SessionGraphScope, op string, raw json.RawMessage) (*GraphExtensionRunner, error) {
	if op != "new" {
		return nil, fmt.Errorf("ExtensionRunner has no static factory %q", op)
	}
	args, err := icodingagent.ParseGraphArgs(raw)
	if err != nil {
		return nil, err
	}
	var options graphExtensionRunnerOptions
	if err := args.Unmarshal(0, func(raw json.RawMessage) error { return json.Unmarshal(raw, &options) }); err != nil {
		return nil, err
	}
	g := &GraphExtensionRunner{scope: scope, cwd: options.Cwd, errorOffs: map[string]func(){}}
	built := false
	defer func() {
		if !built {
			g.releaseHolds()
		}
	}()
	// hold names an object the runner uses, and keeps it for the runner's lifetime.
	hold := func(handle string) (subprocess.SessionGraphObject, bool) {
		object, release, ok := scope.Retain(handle)
		if ok {
			g.holds = append(g.holds, release)
		}
		return object, ok
	}
	var runtime *extension.ExtensionRuntime
	if options.Runtime != "" {
		object, ok := hold(options.Runtime)
		graphRuntime, isRuntime := object.(*GraphExtensionRuntime)
		if !ok || !isRuntime {
			return nil, errors.New(subprocess.SessionGraphUnknownMessage)
		}
		runtime = graphRuntime.runtime
	}
	results := map[string]LoadExtensionsResult{}
	for _, ref := range options.Extensions {
		result, seen := results[ref.Loader]
		if !seen {
			object, ok := hold(ref.Loader)
			loader, isLoader := object.(*GraphResourceLoader)
			if !ok || !isLoader {
				return nil, errors.New(subprocess.SessionGraphUnknownMessage)
			}
			result = loader.loader.GetExtensions()
			results[ref.Loader] = result
			if bridge := result.UIBridge(); bridge != nil && !slices.Contains(g.bridges, bridge) {
				g.bridges = append(g.bridges, bridge)
			}
			if result.host != nil && !slices.Contains(g.hosts, result.host) {
				g.hosts = append(g.hosts, result.host)
			}
		}
		index := slices.IndexFunc(result.Extensions, func(ext extension.Extension) bool { return ext.ResolvedPath == ref.ResolvedPath })
		if index < 0 {
			return nil, fmt.Errorf("the loader holds no extension %s: reload it, then read its extensions again", ref.ResolvedPath)
		}
		g.extensions = append(g.extensions, result.Extensions[index])
	}
	if runtime == nil {
		for _, result := range results {
			if result.Runtime != nil {
				runtime = result.Runtime
				break
			}
		}
	}
	if options.SessionManager != "" {
		object, ok := hold(options.SessionManager)
		manager, isManager := object.(*icodingagent.GraphSessionManager)
		if !ok || !isManager {
			return nil, errors.New(subprocess.SessionGraphUnknownMessage)
		}
		g.session = manager.Session()
	}
	var registry extension.ModelRegistry
	if options.ModelRegistry != "" {
		object, ok := hold(options.ModelRegistry)
		models, isModels := object.(*GraphModelRuntime)
		if !ok || !isModels {
			return nil, errors.New(subprocess.SessionGraphUnknownMessage)
		}
		registry = models.runtime.services.Registry()
	}
	g.runner = NewExtensionRunner(g.extensions, runtime, options.Cwd, g.session, registry)
	g.runtime = g.runner.Runtime()
	built = true
	return g, nil
}

func (g *GraphExtensionRunner) releaseHolds() {
	for _, release := range g.holds {
		release()
	}
	g.holds = nil
}

// Release implements subprocess.SessionGraphReleaser: the runner's error listeners end with it, and so does its hold of the loaders, which stop their extensions when no one else holds them.
func (g *GraphExtensionRunner) Release() {
	defer g.releaseHolds()
	g.mu.Lock()
	offs := g.errorOffs
	g.errorOffs = map[string]func(){}
	g.mu.Unlock()
	for _, off := range offs {
		off()
	}
}

var _ subprocess.SessionGraphReleaser = (*GraphExtensionRunner)(nil)

// callbackArgs is the JSON array of a callback's arguments, in the encoding Pi's JSON.stringify gives.
func callbackArgs(args ...any) (json.RawMessage, error) {
	out := []byte{'['}
	for i, arg := range args {
		if i > 0 {
			out = append(out, ',')
		}
		var err error
		if out, err = jsstring.AppendMarshalJSON(out, arg); err != nil {
			return nil, err
		}
	}
	return json.RawMessage(append(out, ']')), nil
}

// callback runs the extension's function id with args and answers its JSON result.
func (g *GraphExtensionRunner) callback(ctx context.Context, id string, args ...any) (json.RawMessage, error) {
	encoded, err := callbackArgs(args...)
	if err != nil {
		return nil, err
	}
	return g.scope.Callback(ctx, id, encoded)
}

// callbackBackground is the context of a function Pi's bindCore calls synchronously: it carries no signal.
func callbackBackground() context.Context {
	//portlint:allow ctxbackground Pi's bound actions are synchronous functions of the runner, which carry no signal
	return context.Background()
}

// graphScopedModel is one entry of getScopedModels: a model object and its thinking level.
type graphScopedModel struct {
	Model         json.RawMessage `json:"model"`
	ThinkingLevel string          `json:"thinkingLevel"`
}

// graphSignalState is the answer of getSignal: whether the signal has aborted.
type graphSignalState struct {
	Aborted bool `json:"aborted"`
}

// graphRendererFlags tells which render functions a tool renderer registered.
type graphRendererFlags struct {
	RenderCall   bool `json:"renderCall"`
	RenderResult bool `json:"renderResult"`
}

// graphDecoded is every type an extension's answer to a runner callback decodes into. The set is closed so that no answer is decoded into an interface value.
type graphDecoded interface {
	string | bool | []string | extension.ThinkingLevel | extension.Settings | extension.AgentToolCallOutcome | extension.CancelledResult | extension.BoundaryContextPreview |
		[]extension.ToolInfo | []extension.SlashCommandInfo | []extension.AgentTool | []graphScopedModel |
		*extension.ContextUsage | *extension.BuildSystemPromptOptions | *graphSignalState | *graphRendererFlags
}

func decodeJSON[T graphDecoded](raw json.RawMessage) (T, error) {
	var value T
	if len(raw) == 0 {
		return value, nil
	}
	err := json.Unmarshal(raw, &value)
	return value, err
}

// Call runs an ExtensionRunner method.
func (g *GraphExtensionRunner) Call(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	args, err := icodingagent.ParseGraphArgs(raw)
	if err != nil {
		return nil, err
	}
	runner := g.runner
	switch method {
	case "getExtensionPaths":
		return runner.ExtensionPaths(), nil
	case "getAllRegisteredTools":
		tools := runner.Tools()
		for i := range tools {
			// Upstream's registerTool stamps the registering extension's sourceInfo on the tool.
			if info, ok := runner.ToolSourceInfo(tools[i].Definition.Name); ok {
				tools[i].SourceInfo = info
			}
		}
		return nonNilSlice(tools), nil
	case "getToolDefinition":
		name, err := args.String(0)
		if err != nil {
			return nil, err
		}
		if definition, ok := runner.GetToolDefinition(name); ok {
			return definition, nil
		}
		return nil, nil
	case "getFlags":
		flags := runner.Flags()
		out := map[string]extension.ExtensionFlag{}
		for _, ext := range g.extensions {
			for _, flag := range ext.RegisteredFlags() {
				if _, seen := out[flag.Name]; !seen {
					out[flag.Name] = flags[flag.Name]
				}
			}
		}
		return out, nil
	case "setFlagValue":
		name, err := args.String(0)
		if err != nil {
			return nil, err
		}
		value, err := graphFlagValueArg(args, 1)
		if err != nil {
			return nil, err
		}
		runner.SetFlagValue(name, value)
		return nil, nil
	case "getFlagValues":
		return graphFlagValues(runner.GetFlagValues()), nil
	case "getShortcuts":
		var keybindings map[string][]string
		if err := args.Unmarshal(0, func(raw json.RawMessage) error { return decodeKeybindings(raw, &keybindings) }); err != nil {
			return nil, err
		}
		entries := runner.Shortcuts(keybindings)
		out := make([][2]any, len(entries))
		for i, entry := range entries {
			out[i] = [2]any{entry.Key, entry.Shortcut}
		}
		return out, nil
	case "getShortcutDiagnostics":
		return nonNilSlice(runner.ShortcutDiagnostics()), nil
	case "getRegisteredCommands":
		return nonNilSlice(runner.Commands()), nil
	case "getCommandDiagnostics":
		return nonNilSlice(runner.CommandDiagnostics()), nil
	case "getCommand":
		name, err := args.String(0)
		if err != nil {
			return nil, err
		}
		if command, ok := runner.Command(name); ok {
			return command, nil
		}
		return nil, nil
	case "hasHandlers":
		eventType, err := args.String(0)
		if err != nil {
			return nil, err
		}
		return runner.HasHandlers(eventType), nil
	case "getMessageRenderer":
		customType, err := args.String(0)
		if err != nil {
			return nil, err
		}
		return runner.MessageRenderer(customType) != nil, nil
	case "getEntryRenderer":
		customType, err := args.String(0)
		if err != nil {
			return nil, err
		}
		return runner.EntryRenderer(customType) != nil, nil
	case "getMarkdownTransformers":
		return len(runner.GetMarkdownTransformers()), nil
	case "resolveToolRenderers":
		return g.resolveToolRenderers(ctx, args)
	case "hasUI":
		return runner.HasUI(), nil
	case "getActiveTools":
		if err := g.assertActive(); err != nil {
			return nil, err
		}
		if g.runtime.GetActiveTools == nil {
			return nil, extension.ErrRuntimeNotInitialized
		}
		return g.runtime.GetActiveTools(), nil
	case "invalidate":
		message, err := args.OptString(0)
		if err != nil {
			return nil, err
		}
		if message == nil {
			runner.Invalidate(extension.DefaultStaleRuntimeMessage)
		} else {
			runner.Invalidate(*message)
		}
		return nil, nil
	case "shutdown":
		runner.Shutdown()
		return nil, nil
	case "reportUnhandledMcpServers":
		runner.ReportUnhandledMcpServers()
		return nil, nil
	case "onError":
		return g.onError(args)
	case "offError":
		id, err := args.String(0)
		if err != nil {
			return nil, err
		}
		g.mu.Lock()
		off := g.errorOffs[id]
		delete(g.errorOffs, id)
		g.mu.Unlock()
		if off != nil {
			off()
		}
		return nil, nil
	case "emitError":
		var event extension.ExtensionError
		if err := args.Unmarshal(0, func(raw json.RawMessage) error { return json.Unmarshal(raw, &event) }); err != nil {
			return nil, err
		}
		runner.EmitError(&event)
		return nil, nil
	case "bindCore":
		return nil, g.bindCore(args)
	case "bindCommandContext":
		return nil, g.bindCommandContext(args)
	case "setUIContext":
		return nil, g.setUIContext(args)
	case "ui":
		return g.callUI(ctx, args)
	case "context":
		return g.callContext(ctx, args)
	}
	return g.callEmit(ctx, method, args)
}

// nonNilSlice is the JSON array of list; a nil list is an empty array, as Pi's lists are.
func nonNilSlice[T any](list []T) []T {
	if list == nil {
		return []T{}
	}
	return list
}

func (g *GraphExtensionRunner) assertActive() error {
	if g.runner.IsStale() {
		return errors.New(g.runner.StaleMessage())
	}
	return nil
}

// onError is `onError(listener)`: the listener is the extension's function, called with each error the runner reports.
func (g *GraphExtensionRunner) onError(args icodingagent.GraphArgs) (any, error) {
	id, err := args.String(0)
	if err != nil {
		return nil, err
	}
	off := g.runner.AddErrorListener(func(report *extension.ExtensionError) {
		//portlint:allow ctxbackground Pi's error listeners are synchronous functions of emitError, which carries no signal
		_, _ = g.callback(context.Background(), id, report)
	})
	g.mu.Lock()
	g.nextError++
	handle := "e" + strconv.FormatUint(g.nextError, 10)
	g.errorOffs[handle] = off
	g.mu.Unlock()
	return handle, nil
}

// decodeKeybindings is Pi's KeybindingsConfig: each action names a key or a list of keys.
func decodeKeybindings(raw json.RawMessage, out *map[string][]string) error {
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(raw, &wire); err != nil {
		return err
	}
	keybindings := make(map[string][]string, len(wire))
	for _, action := range slices.Sorted(maps.Keys(wire)) {
		var keys []string
		var key string
		switch {
		case json.Unmarshal(wire[action], &key) == nil:
			keys = []string{key}
		case json.Unmarshal(wire[action], &keys) == nil:
		case string(wire[action]) == "null":
			continue
		default:
			return fmt.Errorf("keybinding %s is a key or a list of keys", action)
		}
		keybindings[action] = keys
	}
	*out = keybindings
	return nil
}

// resolveToolRenderers is `resolveToolRenderers(toolName, base)`: base is the extension's function returning the renderers the caller would use after the extensions', as {renderCall, renderResult} presence flags.
func (g *GraphExtensionRunner) resolveToolRenderers(ctx context.Context, args icodingagent.GraphArgs) (any, error) {
	toolName, err := args.String(0)
	if err != nil {
		return nil, err
	}
	baseID, err := args.String(1)
	if err != nil {
		return nil, err
	}
	var baseErr error
	renderers := g.runner.ResolveToolRenderers(toolName, func() *extension.ToolRenderers {
		if baseID == "" {
			return nil
		}
		raw, err := g.callback(ctx, baseID)
		if err != nil {
			baseErr = err
			return nil
		}
		flags, err := decodeJSON[*graphRendererFlags](raw)
		if err != nil {
			baseErr = err
			return nil
		}
		if flags == nil {
			return nil
		}
		out := &extension.ToolRenderers{}
		if flags.RenderCall {
			out.RenderCall = func(json.RawMessage, extension.Theme, extension.ToolRenderContext) extension.Component { return nil }
		}
		if flags.RenderResult {
			out.RenderResult = func(extension.AgentToolResult, extension.ToolRenderResultOptions, extension.Theme, extension.ToolRenderContext) extension.Component {
				return nil
			}
		}
		return out
	})
	if baseErr != nil {
		return nil, baseErr
	}
	if renderers == nil {
		return nil, nil
	}
	out := map[string]any{"renderCall": renderers.RenderCall != nil, "renderResult": renderers.RenderResult != nil}
	if renderers.RenderShell != "" {
		out["renderShell"] = renderers.RenderShell
	}
	return out, nil
}
