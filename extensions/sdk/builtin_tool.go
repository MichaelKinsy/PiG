// SPDX-License-Identifier: MIT

package sdk

// Ports packages/coding-agent/src/core/tools/{read,write,edit,bash,powershell,grep,find,ls}.ts (create<Tool>ToolDefinition, create<Tool>Tool, createCodingTools,
// createReadOnlyTools, createLocalBashOperations, createLocalPowerShellOperations), exported from the package index.
//
// Pi's factories build the built-in tool in the extension's own process. Here the host owns the implementation, so the factories reach it through host
// calls: the definition's metadata (builtinTool.definition), its `execute` (builtinTool.execute) and the local shell operations (builtinTool.localExec). An
// override that wraps a built-in is written as in Pi:
//
//	def, err := sdk.CreateReadToolDefinition(cwd, nil)
//	run := def.Execute
//	def.Execute = func(ctx sdk.Context, params map[string]any) (any, error) {
//	    result, err := run(ctx, params)
//	    ...post-process...
//	}
//	ext.RegisterTool(def.ToolDefinition)
//
// Async contract (Pi's `execute` returns a Promise): the call is awaited, so Execute returns when the tool has finished, with the tool's result, or with the
// error the tool threw. `signal` is a [context.Context]: Execute forwards the calling request's cancellation, which Pi's `execute(...args)` forwards as its
// own signal; ExecuteWith takes the signal explicitly, and nil is Pi's undefined (the call is not cancelled by the request). onUpdate runs on its own goroutine,
// in order and before the call returns. Pi's `operations` and `spawnHook` functions stay in this process and run while the call does.

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// Wire names of the built-in tool calls (coding/extension/host/subprocess/protocol_builtin_tool.go).
const (
	callBuiltinToolDefinition  = "builtinTool.definition"
	callBuiltinToolExecute     = "builtinTool.execute"
	callBuiltinToolLocalExec   = "builtinTool.localExec"
	callExecuteToolCancel      = "executeTool.cancel"
	requestBuiltinToolCallback = "builtin_tool_callback"
	requestBuiltinToolExecData = "builtin_tool_exec_data"
)

// ModelImageResizeOptions is Pi's ModelImageResizeOptions (pi-ai types): the resize profile of an image sent to a model. A zero member is Pi's undefined.
type ModelImageResizeOptions struct {
	MaxWidth  int `json:"maxWidth,omitempty"`
	MaxHeight int `json:"maxHeight,omitempty"`
	// MaxBytes limits the base64-encoded image payload.
	MaxBytes    int `json:"maxBytes,omitempty"`
	JPEGQuality int `json:"jpegQuality,omitempty"`
}

// ReadOperations is Pi's ReadOperations: pluggable file reading, for example over SSH. ReadFile and Access are required; DetectImageMimeType is optional,
// and without it every file is read as text. An empty MIME type is Pi's null or undefined: not an image.
type ReadOperations struct {
	ReadFile            func(absolutePath string) ([]byte, error)
	Access              func(absolutePath string) error
	DetectImageMimeType func(absolutePath string) (string, error)
}

// ReadToolOptions is Pi's ReadToolOptions.
type ReadToolOptions struct {
	// AutoResizeImages resizes images before they reach the model. Nil is Pi's default, true.
	AutoResizeImages *bool
	// ResizeOptions is the fallback resize profile when the execution context has no model metadata.
	ResizeOptions *ModelImageResizeOptions
	// Operations delegates file reading. Nil is the local file system.
	Operations *ReadOperations
}

// WriteOperations is Pi's WriteOperations. Both members are required.
type WriteOperations struct {
	WriteFile func(absolutePath, content string) error
	Mkdir     func(dir string) error
}

// WriteToolOptions is Pi's WriteToolOptions.
type WriteToolOptions struct {
	Operations *WriteOperations
}

// EditOperations is Pi's EditOperations. Every member is required.
type EditOperations struct {
	ReadFile  func(absolutePath string) ([]byte, error)
	WriteFile func(absolutePath, content string) error
	// Access fails when the file is not readable and writable.
	Access func(absolutePath string) error
}

// EditToolOptions is Pi's EditToolOptions.
type EditToolOptions struct {
	Operations *EditOperations
}

// GrepOperations is Pi's GrepOperations. IsDirectory is required and fails when the path does not exist; ReadFile is optional and, without it, matched files
// are streamed from the local file system.
type GrepOperations struct {
	IsDirectory func(absolutePath string) (bool, error)
	ReadFile    func(absolutePath string) (string, error)
}

// GrepToolOptions is Pi's GrepToolOptions.
type GrepToolOptions struct {
	Operations *GrepOperations
}

// FindGlobOptions are the options Pi's FindOperations.glob receives.
type FindGlobOptions struct {
	Ignore []string
	Limit  float64
}

// FindOperations is Pi's FindOperations. Both members are required. Glob returns paths relative to cwd or absolute.
type FindOperations struct {
	Exists func(absolutePath string) (bool, error)
	Glob   func(pattern, cwd string, options FindGlobOptions) ([]string, error)
}

// FindToolOptions is Pi's FindToolOptions.
type FindToolOptions struct {
	Operations *FindOperations
}

// LsStat is the stat Pi's LsOperations.stat returns: only `isDirectory()` is read.
type LsStat struct {
	IsDirectory bool
}

// LsOperations is Pi's LsOperations. Every member is required. Stat fails when the path is not found.
type LsOperations struct {
	Exists  func(absolutePath string) (bool, error)
	Stat    func(absolutePath string) (LsStat, error)
	Readdir func(absolutePath string) ([]string, error)
}

// LsToolOptions is Pi's LsToolOptions.
type LsToolOptions struct {
	Operations *LsOperations
}

// BashSpawnContext is Pi's BashSpawnContext: the command, working directory and environment a shell tool is about to run.
type BashSpawnContext struct {
	Command string            `json:"command"`
	Cwd     string            `json:"cwd"`
	Env     map[string]string `json:"env"`
}

// BashSpawnHook is Pi's BashSpawnHook: it receives the context the command is about to run in and returns the context to run. An error is the hook's throw: the
// tool call fails with it.
type BashSpawnHook func(context BashSpawnContext) (BashSpawnContext, error)

// BashToolOptions is Pi's BashToolOptions.
type BashToolOptions struct {
	// Operations delegates command execution. Nil is the local shell.
	Operations *BashOperations
	// CommandPrefix is prepended to every command (for example shell setup commands).
	CommandPrefix string
	// ShellPath is an explicit shell path.
	ShellPath string
	// ExposeSessionEnvironment exposes the session as PI_* environment variables. Nil is Pi's default, true.
	ExposeSessionEnvironment *bool
	// SpawnHook adjusts the command, working directory or environment before execution.
	SpawnHook BashSpawnHook
}

// PowerShellSpawnContext is Pi's PowerShellSpawnContext, an alias of [BashSpawnContext].
type PowerShellSpawnContext = BashSpawnContext

// PowerShellSpawnHook is Pi's PowerShellSpawnHook, an alias of [BashSpawnHook].
type PowerShellSpawnHook = BashSpawnHook

// PowerShellOperations is Pi's PowerShellOperations, an alias of [BashOperations].
type PowerShellOperations = BashOperations

// PowerShellToolOptions is Pi's PowerShellToolOptions: BashToolOptions' operations, exposeSessionEnvironment and spawnHook.
type PowerShellToolOptions struct {
	Operations               *PowerShellOperations
	ExposeSessionEnvironment *bool
	SpawnHook                PowerShellSpawnHook
}

// ToolsOptions is Pi's ToolsOptions: the options of each built-in tool, for [CreateCodingTools] and [CreateReadOnlyTools].
type ToolsOptions struct {
	Read       *ReadToolOptions
	Bash       *BashToolOptions
	Powershell *PowerShellToolOptions
	Write      *WriteToolOptions
	Edit       *EditToolOptions
	Grep       *GrepToolOptions
	Find       *FindToolOptions
	Ls         *LsToolOptions
}

// builtinCallbacks are the functions of one call's options, which stay in this process. The host calls them with requestBuiltinToolCallback under the handle the
// call carries.
type builtinCallbacks struct {
	read      *ReadOperations
	write     *WriteOperations
	edit      *EditOperations
	grep      *GrepOperations
	find      *FindOperations
	ls        *LsOperations
	bash      *BashOperations
	spawnHook BashSpawnHook
}

// builtinWireOptions is BuiltinToolOptions of the wire: the serializable options, and the names of the functions that stay.
type builtinWireOptions struct {
	AutoResizeImages         *bool                    `json:"autoResizeImages,omitempty"`
	ResizeOptions            *ModelImageResizeOptions `json:"resizeOptions,omitempty"`
	CommandPrefix            string                   `json:"commandPrefix,omitempty"`
	ShellPath                string                   `json:"shellPath,omitempty"`
	ExposeSessionEnvironment *bool                    `json:"exposeSessionEnvironment,omitempty"`
	Operations               []string                 `json:"operations,omitempty"`
	SpawnHook                bool                     `json:"spawnHook,omitempty"`
}

// builtinSpec is one create<Tool>ToolDefinition(cwd, options) call: which tool, where, and with what options.
type builtinSpec struct {
	name      string
	cwd       string
	options   builtinWireOptions
	callbacks *builtinCallbacks
}

// requireMembers fails for a required member an operations object lacks, which Pi's types make a compile error.
func requireMembers(tool string, members map[string]bool) error {
	for _, name := range slices.Sorted(maps.Keys(members)) {
		if !members[name] {
			return fmt.Errorf("%s: operations.%s is required", tool, name)
		}
	}
	return nil
}

func (s *builtinSpec) setOperations(members ...string) {
	s.options.Operations = members
	if s.callbacks == nil {
		s.callbacks = &builtinCallbacks{}
	}
}

func readSpec(cwd string, options *ReadToolOptions) (builtinSpec, error) {
	spec := builtinSpec{name: "read", cwd: cwd}
	if options == nil {
		return spec, nil
	}
	spec.options.AutoResizeImages, spec.options.ResizeOptions = options.AutoResizeImages, options.ResizeOptions
	if ops := options.Operations; ops != nil {
		if err := requireMembers("read", map[string]bool{"readFile": ops.ReadFile != nil, "access": ops.Access != nil}); err != nil {
			return spec, err
		}
		spec.setOperations("readFile", "access")
		if ops.DetectImageMimeType != nil {
			spec.options.Operations = append(spec.options.Operations, "detectImageMimeType")
		}
		spec.callbacks.read = ops
	}
	return spec, nil
}

func writeSpec(cwd string, options *WriteToolOptions) (builtinSpec, error) {
	spec := builtinSpec{name: "write", cwd: cwd}
	if options != nil && options.Operations != nil {
		ops := options.Operations
		if err := requireMembers("write", map[string]bool{"writeFile": ops.WriteFile != nil, "mkdir": ops.Mkdir != nil}); err != nil {
			return spec, err
		}
		spec.setOperations("writeFile", "mkdir")
		spec.callbacks.write = ops
	}
	return spec, nil
}

func editSpec(cwd string, options *EditToolOptions) (builtinSpec, error) {
	spec := builtinSpec{name: "edit", cwd: cwd}
	if options != nil && options.Operations != nil {
		ops := options.Operations
		if err := requireMembers("edit", map[string]bool{"readFile": ops.ReadFile != nil, "writeFile": ops.WriteFile != nil, "access": ops.Access != nil}); err != nil {
			return spec, err
		}
		spec.setOperations("readFile", "writeFile", "access")
		spec.callbacks.edit = ops
	}
	return spec, nil
}

func grepSpec(cwd string, options *GrepToolOptions) (builtinSpec, error) {
	spec := builtinSpec{name: "grep", cwd: cwd}
	if options != nil && options.Operations != nil {
		ops := options.Operations
		if err := requireMembers("grep", map[string]bool{"isDirectory": ops.IsDirectory != nil}); err != nil {
			return spec, err
		}
		spec.setOperations("isDirectory")
		if ops.ReadFile != nil {
			spec.options.Operations = append(spec.options.Operations, "readFile")
		}
		spec.callbacks.grep = ops
	}
	return spec, nil
}

func findSpec(cwd string, options *FindToolOptions) (builtinSpec, error) {
	spec := builtinSpec{name: "find", cwd: cwd}
	if options != nil && options.Operations != nil {
		ops := options.Operations
		if err := requireMembers("find", map[string]bool{"exists": ops.Exists != nil, "glob": ops.Glob != nil}); err != nil {
			return spec, err
		}
		spec.setOperations("exists", "glob")
		spec.callbacks.find = ops
	}
	return spec, nil
}

func lsSpec(cwd string, options *LsToolOptions) (builtinSpec, error) {
	spec := builtinSpec{name: "ls", cwd: cwd}
	if options != nil && options.Operations != nil {
		ops := options.Operations
		if err := requireMembers("ls", map[string]bool{"exists": ops.Exists != nil, "stat": ops.Stat != nil, "readdir": ops.Readdir != nil}); err != nil {
			return spec, err
		}
		spec.setOperations("exists", "stat", "readdir")
		spec.callbacks.ls = ops
	}
	return spec, nil
}

func shellSpec(name, cwd string, operations *BashOperations, hook BashSpawnHook, expose *bool) (builtinSpec, error) {
	spec := builtinSpec{name: name, cwd: cwd}
	spec.options.ExposeSessionEnvironment = expose
	if operations != nil {
		if err := requireMembers(name, map[string]bool{"exec": operations.Exec != nil}); err != nil {
			return spec, err
		}
		spec.setOperations("exec")
		spec.callbacks.bash = operations
	}
	if hook != nil {
		if spec.callbacks == nil {
			spec.callbacks = &builtinCallbacks{}
		}
		spec.options.SpawnHook = true
		spec.callbacks.spawnHook = hook
	}
	return spec, nil
}

func bashSpec(cwd string, options *BashToolOptions) (builtinSpec, error) {
	if options == nil {
		return builtinSpec{name: "bash", cwd: cwd}, nil
	}
	spec, err := shellSpec("bash", cwd, options.Operations, options.SpawnHook, options.ExposeSessionEnvironment)
	spec.options.CommandPrefix, spec.options.ShellPath = options.CommandPrefix, options.ShellPath
	return spec, err
}

func powershellSpec(cwd string, options *PowerShellToolOptions) (builtinSpec, error) {
	if options == nil {
		return builtinSpec{name: "powershell", cwd: cwd}, nil
	}
	return shellSpec("powershell", cwd, options.Operations, options.SpawnHook, options.ExposeSessionEnvironment)
}

// BuiltinToolDefinition is the ToolDefinition Pi's create<Tool>ToolDefinition returns for a built-in tool: its name, label, description, prompt snippet and
// guidelines, schemas and execution mode, with an Execute that runs the built-in tool on the host. Register the embedded [ToolDefinition] to override the
// built-in, changing fields or Execute first to wrap it.
//
// The host draws the built-in tool's call and result itself, so a definition that sets no renderers keeps the built-in's, as an override of a built-in does in Pi.
type BuiltinToolDefinition struct {
	ToolDefinition
	spec builtinSpec
}

// Execute (the embedded ToolDefinition's field) is Pi's `definition.execute(...args)` of a tool handler: the tool call id, the calling request's
// cancellation as the signal, the request's update sink as onUpdate and the extension's context are the handler's own. ExecuteWith passes each explicitly.

// ExecuteWith is Pi's `definition.execute(toolCallId, params, signal, onUpdate, ctx)`. A nil signal is Pi's undefined: only the call itself ends it. onUpdate
// receives each partial result, in order and before the call returns, on its own goroutine; a panic in it is the callback's throw and fails the call with its
// message after the tool returned. The tool reads the session's working directory, model and identity from ctx, as `ctx?.cwd || cwd` and `ctx?.model` do;
// the cwd given to the factory applies only to a tool without a context ([BuiltinTool]).
func (d BuiltinToolDefinition) ExecuteWith(ctx Context, toolCallID string, params any, signal context.Context, onUpdate func(partial AgentToolResult)) (AgentToolResult, error) {
	return d.spec.execute(&ctx, toolCallID, params, signal, false, onUpdate)
}

// BuiltinTool is the AgentTool Pi's create<Tool>Tool returns: the built-in tool without an extension context, so it works in the working directory given to the
// factory and without the session's model and identity.
type BuiltinTool struct {
	Name                string
	Label               string
	Description         string
	Parameters          Schema
	OutputSchema        Schema
	ConstrainedSampling ToolConstrainedSampling
	ExecutionMode       string
	// PromptSnippet and PromptGuidelines are set for the shell tools, as Pi's createBashTool and createPowerShellTool do.
	PromptSnippet    string
	PromptGuidelines []string
	spec             builtinSpec
}

// Execute is Pi's `tool.execute(toolCallId, params, signal, onUpdate)`. It runs over the connection of the running extension, so it needs [Extension.Run]
// to have started: a call before that is an error. A nil signal is Pi's undefined.
func (t BuiltinTool) Execute(toolCallID string, params any, signal context.Context, onUpdate func(partial AgentToolResult)) (AgentToolResult, error) {
	return t.spec.execute(nil, toolCallID, params, signal, false, onUpdate)
}

// builtinDefinitionReply is the reply of builtinTool.definition.
type builtinDefinitionReply struct {
	Name                string          `json:"name"`
	Label               string          `json:"label"`
	Description         string          `json:"description"`
	PromptSnippet       string          `json:"promptSnippet"`
	PromptGuidelines    []string        `json:"promptGuidelines"`
	Parameters          Schema          `json:"parameters"`
	OutputSchema        Schema          `json:"outputSchema"`
	ConstrainedSampling json.RawMessage `json:"constrainedSampling"`
	ExecutionMode       string          `json:"executionMode"`
	RenderShell         string          `json:"renderShell"`
}

// definition asks the host for the tool's metadata. It may run before Run, like [ParseFrontmatter].
func (s builtinSpec) definition() (builtinDefinitionReply, error) {
	args := struct {
		Name    string              `json:"name"`
		Cwd     string              `json:"cwd"`
		Options *builtinWireOptions `json:"options,omitempty"`
	}{s.name, s.cwd, &s.options}
	var reply builtinDefinitionReply
	if hook := installedHelpers(); hook != nil && hook.BuiltinToolDefinition != nil {
		raw, err := json.MarshalJS(args)
		if err != nil {
			return reply, err
		}
		encoded, err := hook.BuiltinToolDefinition(raw)
		if err != nil {
			return reply, err
		}
		return reply, json.Unmarshal(encoded, &reply)
	}
	c, err := helperConnection()
	if err != nil {
		return reply, err
	}
	result, err := c.call(callBuiltinToolDefinition, args)
	if err := callResultError(result, err); err != nil {
		return reply, err
	}
	if result == nil {
		return reply, errors.New("host returned no result for builtinTool.definition")
	}
	if err := json.Unmarshal(result.Result, &reply); err != nil {
		return reply, fmt.Errorf("host reply to builtinTool.definition: %w", err)
	}
	return reply, nil
}

func (s builtinSpec) toolDefinition() (BuiltinToolDefinition, error) {
	reply, err := s.definition()
	if err != nil {
		return BuiltinToolDefinition{}, err
	}
	def := BuiltinToolDefinition{spec: s}
	def.Name, def.Label, def.Description = reply.Name, reply.Label, reply.Description
	def.PromptSnippet, def.PromptGuidelines = reply.PromptSnippet, reply.PromptGuidelines
	def.Parameters, def.OutputSchema = reply.Parameters, reply.OutputSchema
	def.ExecutionMode = reply.ExecutionMode
	if reply.RenderShell == string(ToolRenderShellSelf) {
		def.RenderShell = ToolRenderShellSelf
	}
	if len(reply.ConstrainedSampling) > 0 {
		var sampling ConstrainedSampling
		if err := json.Unmarshal(reply.ConstrainedSampling, &sampling); err != nil {
			return def, fmt.Errorf("host reply to builtinTool.definition: constrainedSampling: %w", err)
		}
		def.ConstrainedSampling = &sampling
	}
	spec := s
	def.Execute = func(ctx Context, params map[string]any) (any, error) {
		var forward func(AgentToolResult)
		if ctx.toolCallID != "" && ctx.requestID != "" {
			forward = func(partial AgentToolResult) {
				if err := ctx.OnUpdate(partial); err != nil {
					panic(err)
				}
			}
		}
		result, err := spec.execute(&ctx, ctx.toolCallID, params, nil, true, forward)
		if err != nil {
			return nil, err
		}
		return result, nil
	}
	return def, nil
}

func (s builtinSpec) tool() (BuiltinTool, error) {
	reply, err := s.definition()
	if err != nil {
		return BuiltinTool{}, err
	}
	tool := BuiltinTool{Name: reply.Name, Label: reply.Label, Description: reply.Description, Parameters: reply.Parameters, OutputSchema: reply.OutputSchema, ExecutionMode: reply.ExecutionMode, spec: s}
	if len(reply.ConstrainedSampling) > 0 {
		var sampling ConstrainedSampling
		if err := json.Unmarshal(reply.ConstrainedSampling, &sampling); err != nil {
			return tool, fmt.Errorf("host reply to builtinTool.definition: constrainedSampling: %w", err)
		}
		tool.ConstrainedSampling = &sampling
	}
	if s.name == "bash" || s.name == "powershell" {
		tool.PromptSnippet, tool.PromptGuidelines = reply.PromptSnippet, reply.PromptGuidelines
	}
	return tool, nil
}

// builtinRegistry holds, for the calls in flight, the functions the host calls back: the options' callbacks by handle and the output sinks of local
// executions by execute id. Ids are unique across the process, so one table serves every extension of a packed cell.
var builtinRegistry struct {
	mu        sync.Mutex
	seq       atomic.Uint64
	callbacks map[string]*builtinCallbacks
	execData  map[string]func([]byte) error
}

func registerBuiltinCallbacks(callbacks *builtinCallbacks) (handle string, release func()) {
	handle = fmt.Sprintf("builtin-%d", builtinRegistry.seq.Add(1))
	builtinRegistry.mu.Lock()
	if builtinRegistry.callbacks == nil {
		builtinRegistry.callbacks = map[string]*builtinCallbacks{}
	}
	builtinRegistry.callbacks[handle] = callbacks
	builtinRegistry.mu.Unlock()
	return handle, func() {
		builtinRegistry.mu.Lock()
		delete(builtinRegistry.callbacks, handle)
		builtinRegistry.mu.Unlock()
	}
}

// runningExtension is the extension a call made outside a handler goes through: the running one the connection helpers pick.
func runningExtension() (*Extension, error) {
	if _, err := helperConnection(); err != nil {
		return nil, err
	}
	helpers.mu.Lock()
	defer helpers.mu.Unlock()
	path := helperSocketPath()
	for _, running := range helpers.running {
		if (path == "" || running.path == "" || running.path == path) && running.ext != nil {
			return running.ext, nil
		}
	}
	return nil, errors.New("the extension is not running: a built-in tool without a context can execute only after Run has started")
}

// execute is `execute(toolCallId, params, signal, onUpdate, ctx)` of a built-in tool's definition. ctx nil is an undefined context. forwardSignal makes the
// calling request's cancellation the signal.
func (s builtinSpec) execute(ctx *Context, toolCallID string, params any, signal context.Context, forwardSignal bool, onUpdate func(AgentToolResult)) (AgentToolResult, error) {
	rawParams, err := json.MarshalJS(params)
	if err != nil {
		return AgentToolResult{}, fmt.Errorf("encode arguments of %s: %w", s.name, err)
	}
	var ext *Extension
	if ctx != nil {
		ext = ctx.ext
	}
	if ext == nil {
		ctx = nil
		// A fused extension shares the host's process, so the host runs the tool for it directly: a call without a Context has no connection of its own, and one
		// that rode the first running extension's would end with that extension, whichever made the call.
		if hook := installedHelpers(); hook != nil && hook.BuiltinToolExecute != nil {
			return s.executeDirect(hook, toolCallID, rawParams, signal, onUpdate)
		}
		if ext, err = runningExtension(); err != nil {
			return AgentToolResult{}, err
		}
	}
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
	var handle string
	if s.callbacks != nil {
		var release func()
		handle, release = registerBuiltinCallbacks(s.callbacks)
		defer release()
		if s.callbacks.bash != nil {
			// The shell tool's exec rides the user_bash exec request under the same handle.
			ext.bashOperationsMu.Lock()
			if ext.bashOperations == nil {
				ext.bashOperations = map[string]BashOperations{}
			}
			ext.bashOperations[handle] = *s.callbacks.bash
			ext.bashOperationsMu.Unlock()
			defer func() {
				ext.bashOperationsMu.Lock()
				delete(ext.bashOperations, handle)
				ext.bashOperationsMu.Unlock()
			}()
		}
	}
	call := builtinExecuteCall{s.name, s.cwd, &s.options, toolCallID, rawParams, executeID, onUpdate != nil, ctx != nil, handle, signal != nil && signal.Err() != nil}

	// A signal other than the request's detaches the call from the request, and aborts it through a second call ordered after the call's frame.
	detached := !forwardSignal
	stop := make(chan struct{})
	var watcher sync.WaitGroup
	sent := func() {
		if signal == nil {
			return
		}
		watcher.Go(func() {
			select {
			case <-signal.Done():
				cancelResult, cancelErr := ext.conn.call(callExecuteToolCancel, struct {
					ExecuteID string `json:"executeId"`
				}{executeID})
				if err := callResultError(cancelResult, cancelErr); err != nil {
					reportHostCallFailure(callExecuteToolCancel, err, nil)
				}
			case <-stop:
			}
		})
	}
	var result *callResultMsg
	var callErr error
	if ctx != nil {
		result, callErr = ctx.callHostAfterBegin(callBuiltinToolExecute, call, detached, sent)
	} else {
		var pending pendingCall
		if pending, callErr = ext.conn.beginCallFor("", callBuiltinToolExecute, call); callErr == nil {
			sent()
			result, callErr = ext.conn.waitCall(pending)
		}
	}
	close(stop)
	watcher.Wait()
	if err := callResultError(result, callErr); err != nil {
		return AgentToolResult{}, err
	}
	if result == nil {
		return AgentToolResult{}, errors.New("host returned no result for builtinTool.execute")
	}
	var out AgentToolResult
	if err := json.Unmarshal(result.Result, &out); err != nil {
		return AgentToolResult{}, fmt.Errorf("host reply to builtinTool.execute: %w", err)
	}
	return out, nil
}

// builtinExecuteCall is the arguments of builtinTool.execute, and the JSON [HostHelpers.BuiltinToolExecute] takes.
type builtinExecuteCall struct {
	Name         string              `json:"name"`
	Cwd          string              `json:"cwd"`
	Options      *builtinWireOptions `json:"options,omitempty"`
	ToolCallID   string              `json:"toolCallId"`
	Params       json.RawMessage     `json:"params"`
	ExecuteID    string              `json:"executeId"`
	WantsUpdates bool                `json:"wantsUpdates,omitempty"`
	Context      bool                `json:"context,omitempty"`
	Callbacks    string              `json:"callbacks,omitempty"`
	Aborted      bool                `json:"aborted,omitempty"`
}

// executeDirect is execute of a call without a Context for a fused extension: the host runs the tool in this process through hook, and reaches the functions of
// the options and onUpdate through peer, which are in this process too. The tool ends with signal.
func (s builtinSpec) executeDirect(hook *HostHelpers, toolCallID string, rawParams []byte, signal context.Context, onUpdate func(AgentToolResult)) (AgentToolResult, error) {
	var handle string
	if s.callbacks != nil {
		var release func()
		handle, release = registerBuiltinCallbacks(s.callbacks)
		defer release()
	}
	call := builtinExecuteCall{Name: s.name, Cwd: s.cwd, Options: &s.options, ToolCallID: toolCallID, Params: rawParams, WantsUpdates: onUpdate != nil, Callbacks: handle, Aborted: signal != nil && signal.Err() != nil}
	raw, err := json.MarshalJS(call)
	if err != nil {
		return AgentToolResult{}, fmt.Errorf("encode arguments of %s: %w", s.name, err)
	}
	if signal == nil {
		signal = context.Background()
	}
	peer := BuiltinToolPeer{
		Callback: func(_ context.Context, _, member string, args []byte) ([]byte, error) {
			result, err := invokeBuiltinCallback(s.callbacks, member, args)
			if err != nil || result == nil {
				return nil, err
			}
			return json.MarshalJS(result)
		},
		Update: func(_ context.Context, _ string, result []byte) (failure error) {
			if onUpdate == nil {
				return nil
			}
			var partial AgentToolResult
			if err := json.Unmarshal(result, &partial); err != nil {
				return fmt.Errorf("decode %s: %w", requestExecuteToolUpdate, err)
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
			onUpdate(partial)
			return nil
		},
		Exec: func(ctx context.Context, _, command, cwd string, timeout *float64, env map[string]string, onData func([]byte)) (*int, error) {
			if s.callbacks == nil || s.callbacks.bash == nil {
				return nil, errors.New("unknown bash operations")
			}
			result, err := s.callbacks.bash.Exec(command, cwd, BashExecOptions{OnData: onData, Signal: ctx, Timeout: timeout, Env: env})
			return result.ExitCode, err
		},
	}
	encoded, err := hook.BuiltinToolExecute(signal, raw, peer)
	if err != nil {
		return AgentToolResult{}, err
	}
	var out AgentToolResult
	if err := json.Unmarshal(encoded, &out); err != nil {
		return AgentToolResult{}, fmt.Errorf("host reply to builtinTool.execute: %w", err)
	}
	return out, nil
}

// dispatchBuiltinToolCallback runs one function of a call's options for the host's requestBuiltinToolCallback.
func (e *Extension) dispatchBuiltinToolCallback(args json.RawMessage) (result any, failure error) {
	var request struct {
		Handle string          `json:"handle"`
		Member string          `json:"member"`
		Args   json.RawMessage `json:"args"`
	}
	if err := json.Unmarshal(args, &request); err != nil {
		return nil, fmt.Errorf("decode %s: %w", requestBuiltinToolCallback, err)
	}
	builtinRegistry.mu.Lock()
	callbacks := builtinRegistry.callbacks[request.Handle]
	builtinRegistry.mu.Unlock()
	if callbacks == nil {
		return nil, fmt.Errorf("unknown built-in tool callbacks: %s", request.Handle)
	}
	return invokeBuiltinCallback(callbacks, request.Member, request.Args)
}

// invokeBuiltinCallback runs one function of a call's options. A throw in the callback is its error.
func invokeBuiltinCallback(callbacks *builtinCallbacks, member string, args json.RawMessage) (result any, failure error) {
	if callbacks == nil {
		return nil, errors.New("unknown built-in tool callbacks")
	}
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
	return callbacks.call(member, args)
}

type pathArg struct {
	Path string `json:"path"`
}

func (c *builtinCallbacks) call(member string, raw json.RawMessage) (any, error) {
	var path pathArg
	var write struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	decode := func(into any) error {
		if err := json.Unmarshal(raw, into); err != nil {
			return fmt.Errorf("decode %s arguments: %w", member, err)
		}
		return nil
	}
	switch member {
	case "operations.readFile":
		if err := decode(&path); err != nil {
			return nil, err
		}
		switch {
		case c.read != nil:
			data, err := c.read.ReadFile(path.Path)
			return base64.StdEncoding.EncodeToString(data), err
		case c.edit != nil:
			data, err := c.edit.ReadFile(path.Path)
			return base64.StdEncoding.EncodeToString(data), err
		case c.grep != nil && c.grep.ReadFile != nil:
			return c.grep.ReadFile(path.Path)
		}
	case "operations.access":
		if err := decode(&path); err != nil {
			return nil, err
		}
		switch {
		case c.read != nil:
			return nil, c.read.Access(path.Path)
		case c.edit != nil:
			return nil, c.edit.Access(path.Path)
		}
	case "operations.detectImageMimeType":
		if err := decode(&path); err != nil {
			return nil, err
		}
		if c.read != nil && c.read.DetectImageMimeType != nil {
			mime, err := c.read.DetectImageMimeType(path.Path)
			if mime == "" {
				return nil, err
			}
			return mime, err
		}
	case "operations.writeFile":
		if err := decode(&write); err != nil {
			return nil, err
		}
		switch {
		case c.write != nil:
			return nil, c.write.WriteFile(write.Path, write.Content)
		case c.edit != nil:
			return nil, c.edit.WriteFile(write.Path, write.Content)
		}
	case "operations.mkdir":
		if err := decode(&path); err != nil {
			return nil, err
		}
		if c.write != nil {
			return nil, c.write.Mkdir(path.Path)
		}
	case "operations.isDirectory":
		if err := decode(&path); err != nil {
			return nil, err
		}
		if c.grep != nil {
			return c.grep.IsDirectory(path.Path)
		}
	case "operations.exists":
		if err := decode(&path); err != nil {
			return nil, err
		}
		switch {
		case c.find != nil:
			return c.find.Exists(path.Path)
		case c.ls != nil:
			return c.ls.Exists(path.Path)
		}
	case "operations.glob":
		var glob struct {
			Pattern string   `json:"pattern"`
			Cwd     string   `json:"cwd"`
			Ignore  []string `json:"ignore"`
			Limit   float64  `json:"limit"`
		}
		if err := decode(&glob); err != nil {
			return nil, err
		}
		if c.find != nil {
			paths, err := c.find.Glob(glob.Pattern, glob.Cwd, FindGlobOptions{Ignore: glob.Ignore, Limit: glob.Limit})
			if paths == nil {
				paths = []string{}
			}
			return paths, err
		}
	case "operations.stat":
		if err := decode(&path); err != nil {
			return nil, err
		}
		if c.ls != nil {
			stat, err := c.ls.Stat(path.Path)
			return map[string]bool{"isDirectory": stat.IsDirectory}, err
		}
	case "operations.readdir":
		if err := decode(&path); err != nil {
			return nil, err
		}
		if c.ls != nil {
			names, err := c.ls.Readdir(path.Path)
			if names == nil {
				names = []string{}
			}
			return names, err
		}
	case "spawnHook":
		var spawn BashSpawnContext
		if err := decode(&spawn); err != nil {
			return nil, err
		}
		if c.spawnHook != nil {
			return c.spawnHook(spawn)
		}
	}
	return nil, fmt.Errorf("no built-in tool callback %s", member)
}

// LocalBashOperationsOptions is the `{ shellPath }` of Pi's createLocalBashOperations.
type LocalBashOperationsOptions struct {
	// ShellPath is an explicit shell path; empty resolves the platform's shell.
	ShellPath string
}

// CreateLocalBashOperations is Pi's createLocalBashOperations(options): the BashOperations that run a command in the local shell, which an extension wraps
// to rewrite commands while it keeps Pi's local shell behavior (for example in a user_bash handler or a bash tool's operations). The command runs on the
// host; Exec streams its output to OnData in order and returns when the command ends, with Pi's `{ exitCode }`, or with the error Pi's exec rejects with
// ("aborted" when Signal was cancelled, "timeout:<seconds>" when Timeout elapsed). Exec runs over the connection of the running extension, so it needs
// [Extension.Run] to have started; a fused extension's Exec runs the command in the host's own process through [HostHelpers.LocalExec] and needs no connection.
func CreateLocalBashOperations(options *LocalBashOperationsOptions) BashOperations {
	var shellPath string
	if options != nil {
		shellPath = options.ShellPath
	}
	return BashOperations{Exec: func(command, cwd string, o BashExecOptions) (BashExecResult, error) {
		return localShellExec("bash", shellPath, command, cwd, o)
	}}
}

// CreateLocalPowerShellOperations is Pi's createLocalPowerShellOperations(): [CreateLocalBashOperations] for PowerShell.
func CreateLocalPowerShellOperations() PowerShellOperations {
	return BashOperations{Exec: func(command, cwd string, o BashExecOptions) (BashExecResult, error) {
		return localShellExec("powershell", "", command, cwd, o)
	}}
}

// localExecCall is the arguments of builtinTool.localExec, and the JSON [HostHelpers.LocalExec] takes.
type localExecCall struct {
	Shell     string             `json:"shell"`
	ShellPath string             `json:"shellPath,omitempty"`
	Command   string             `json:"command"`
	Cwd       string             `json:"cwd"`
	Timeout   *float64           `json:"timeout,omitempty"`
	Env       *map[string]string `json:"env,omitempty"`
	ExecuteID string             `json:"executeId"`
	Aborted   bool               `json:"aborted,omitempty"`
}

// localExecReply is Pi's `{ exitCode }`, the reply of builtinTool.localExec.
type localExecReply struct {
	ExitCode *int `json:"exitCode"`
}

func localShellExec(shell, shellPath, command, cwd string, o BashExecOptions) (BashExecResult, error) {
	var dataErr error
	var dataMu sync.Mutex
	deliver := func(data []byte) (failure error) {
		dataMu.Lock()
		defer dataMu.Unlock()
		if o.OnData == nil || dataErr != nil {
			return dataErr
		}
		// A throw in onData ends the command with it, as Pi's would reject the exec.
		defer func() {
			if recovered := recover(); recovered != nil {
				if err, ok := recovered.(error); ok {
					failure = err
				} else {
					failure = fmt.Errorf("%v", recovered)
				}
				dataErr = failure
			}
		}()
		o.OnData(data)
		return nil
	}
	call := localExecCall{Shell: shell, ShellPath: shellPath, Command: command, Cwd: cwd, Timeout: o.Timeout, Aborted: o.Signal != nil && o.Signal.Err() != nil}
	if o.Env != nil {
		call.Env = &o.Env
	}
	// A fused extension shares the host's process, so the host runs the command for it directly, as Pi's exec runs it in the extension's own process. The call
	// has no Context, so no connection is its own: one that rode the first running extension's would end with that extension, whichever the caller is.
	if hook := installedHelpers(); hook != nil && hook.LocalExec != nil {
		raw, err := json.MarshalJS(call)
		if err != nil {
			return BashExecResult{}, err
		}
		signal := o.Signal
		if signal == nil {
			signal = context.Background()
		}
		encoded, execErr := hook.LocalExec(signal, raw, deliver)
		dataMu.Lock()
		failed := dataErr
		dataMu.Unlock()
		if failed != nil {
			return BashExecResult{}, failed
		}
		if execErr != nil {
			return BashExecResult{}, execErr
		}
		var reply localExecReply
		if err := json.Unmarshal(encoded, &reply); err != nil {
			return BashExecResult{}, fmt.Errorf("host reply to builtinTool.localExec: %w", err)
		}
		return BashExecResult(reply), nil
	}
	ext, err := runningExtension()
	if err != nil {
		return BashExecResult{}, err
	}
	executeID := fmt.Sprintf("execute-%d", ext.executeSeq.Add(1))
	call.ExecuteID = executeID
	builtinRegistry.mu.Lock()
	if builtinRegistry.execData == nil {
		builtinRegistry.execData = map[string]func([]byte) error{}
	}
	builtinRegistry.execData[executeID] = deliver
	builtinRegistry.mu.Unlock()
	defer func() {
		builtinRegistry.mu.Lock()
		delete(builtinRegistry.execData, executeID)
		builtinRegistry.mu.Unlock()
	}()
	stop := make(chan struct{})
	var watcher sync.WaitGroup
	pending, err := ext.conn.beginCallFor("", callBuiltinToolLocalExec, call)
	if err != nil {
		return BashExecResult{}, err
	}
	if o.Signal != nil {
		watcher.Go(func() {
			select {
			case <-o.Signal.Done():
				cancelResult, cancelErr := ext.conn.call(callExecuteToolCancel, struct {
					ExecuteID string `json:"executeId"`
				}{executeID})
				if err := callResultError(cancelResult, cancelErr); err != nil {
					reportHostCallFailure(callExecuteToolCancel, err, nil)
				}
			case <-stop:
			}
		})
	}
	result, callErr := ext.conn.waitCall(pending)
	close(stop)
	watcher.Wait()
	dataMu.Lock()
	failed := dataErr
	dataMu.Unlock()
	if failed != nil {
		return BashExecResult{}, failed
	}
	if err := callResultError(result, callErr); err != nil {
		return BashExecResult{}, err
	}
	if result == nil {
		return BashExecResult{}, errors.New("host returned no result for builtinTool.localExec")
	}
	var reply localExecReply
	if err := json.Unmarshal(result.Result, &reply); err != nil {
		return BashExecResult{}, fmt.Errorf("host reply to builtinTool.localExec: %w", err)
	}
	return BashExecResult(reply), nil
}

// dispatchBuiltinExecData delivers one output chunk of a local execution to its OnData, and returns what the host waits for: nil, or OnData's throw.
func (e *Extension) dispatchBuiltinExecData(args json.RawMessage) error {
	var chunk struct {
		ExecuteID string `json:"executeId"`
		Data      string `json:"data"`
	}
	if err := json.Unmarshal(args, &chunk); err != nil {
		return fmt.Errorf("decode %s: %w", requestBuiltinToolExecData, err)
	}
	builtinRegistry.mu.Lock()
	sink := builtinRegistry.execData[chunk.ExecuteID]
	builtinRegistry.mu.Unlock()
	if sink == nil {
		return nil
	}
	data, err := base64.StdEncoding.DecodeString(chunk.Data)
	if err != nil {
		return fmt.Errorf("decode %s: %w", requestBuiltinToolExecData, err)
	}
	return sink(data)
}

// CreateReadToolDefinition is Pi's createReadToolDefinition(cwd, options). Like every Create*ToolDefinition it asks the host for the built-in tool's
// definition, so it may be called while the factory runs, before [Extension.Run], and it fails when the host cannot be reached.
func CreateReadToolDefinition(cwd string, options *ReadToolOptions) (BuiltinToolDefinition, error) {
	spec, err := readSpec(cwd, options)
	if err != nil {
		return BuiltinToolDefinition{}, err
	}
	return spec.toolDefinition()
}

// CreateWriteToolDefinition is Pi's createWriteToolDefinition(cwd, options).
func CreateWriteToolDefinition(cwd string, options *WriteToolOptions) (BuiltinToolDefinition, error) {
	spec, err := writeSpec(cwd, options)
	if err != nil {
		return BuiltinToolDefinition{}, err
	}
	return spec.toolDefinition()
}

// CreateEditToolDefinition is Pi's createEditToolDefinition(cwd, options).
func CreateEditToolDefinition(cwd string, options *EditToolOptions) (BuiltinToolDefinition, error) {
	spec, err := editSpec(cwd, options)
	if err != nil {
		return BuiltinToolDefinition{}, err
	}
	return spec.toolDefinition()
}

// CreateBashToolDefinition is Pi's createBashToolDefinition(cwd, options).
func CreateBashToolDefinition(cwd string, options *BashToolOptions) (BuiltinToolDefinition, error) {
	spec, err := bashSpec(cwd, options)
	if err != nil {
		return BuiltinToolDefinition{}, err
	}
	return spec.toolDefinition()
}

// CreateGrepToolDefinition is Pi's createGrepToolDefinition(cwd, options).
func CreateGrepToolDefinition(cwd string, options *GrepToolOptions) (BuiltinToolDefinition, error) {
	spec, err := grepSpec(cwd, options)
	if err != nil {
		return BuiltinToolDefinition{}, err
	}
	return spec.toolDefinition()
}

// CreateFindToolDefinition is Pi's createFindToolDefinition(cwd, options).
func CreateFindToolDefinition(cwd string, options *FindToolOptions) (BuiltinToolDefinition, error) {
	spec, err := findSpec(cwd, options)
	if err != nil {
		return BuiltinToolDefinition{}, err
	}
	return spec.toolDefinition()
}

// CreateLsToolDefinition is Pi's createLsToolDefinition(cwd, options).
func CreateLsToolDefinition(cwd string, options *LsToolOptions) (BuiltinToolDefinition, error) {
	spec, err := lsSpec(cwd, options)
	if err != nil {
		return BuiltinToolDefinition{}, err
	}
	return spec.toolDefinition()
}

// CreatePowerShellToolDefinition is Pi's createPowerShellToolDefinition(cwd, options).
func CreatePowerShellToolDefinition(cwd string, options *PowerShellToolOptions) (BuiltinToolDefinition, error) {
	spec, err := powershellSpec(cwd, options)
	if err != nil {
		return BuiltinToolDefinition{}, err
	}
	return spec.toolDefinition()
}

// CreateReadTool is Pi's createReadTool(cwd, options).
func CreateReadTool(cwd string, options *ReadToolOptions) (BuiltinTool, error) {
	spec, err := readSpec(cwd, options)
	if err != nil {
		return BuiltinTool{}, err
	}
	return spec.tool()
}

// CreateWriteTool is Pi's createWriteTool(cwd, options).
func CreateWriteTool(cwd string, options *WriteToolOptions) (BuiltinTool, error) {
	spec, err := writeSpec(cwd, options)
	if err != nil {
		return BuiltinTool{}, err
	}
	return spec.tool()
}

// CreateEditTool is Pi's createEditTool(cwd, options).
func CreateEditTool(cwd string, options *EditToolOptions) (BuiltinTool, error) {
	spec, err := editSpec(cwd, options)
	if err != nil {
		return BuiltinTool{}, err
	}
	return spec.tool()
}

// CreateBashTool is Pi's createBashTool(cwd, options).
func CreateBashTool(cwd string, options *BashToolOptions) (BuiltinTool, error) {
	spec, err := bashSpec(cwd, options)
	if err != nil {
		return BuiltinTool{}, err
	}
	return spec.tool()
}

// CreateGrepTool is Pi's createGrepTool(cwd, options).
func CreateGrepTool(cwd string, options *GrepToolOptions) (BuiltinTool, error) {
	spec, err := grepSpec(cwd, options)
	if err != nil {
		return BuiltinTool{}, err
	}
	return spec.tool()
}

// CreateFindTool is Pi's createFindTool(cwd, options).
func CreateFindTool(cwd string, options *FindToolOptions) (BuiltinTool, error) {
	spec, err := findSpec(cwd, options)
	if err != nil {
		return BuiltinTool{}, err
	}
	return spec.tool()
}

// CreateLsTool is Pi's createLsTool(cwd, options).
func CreateLsTool(cwd string, options *LsToolOptions) (BuiltinTool, error) {
	spec, err := lsSpec(cwd, options)
	if err != nil {
		return BuiltinTool{}, err
	}
	return spec.tool()
}

// CreatePowerShellTool is Pi's createPowerShellTool(cwd, options).
func CreatePowerShellTool(cwd string, options *PowerShellToolOptions) (BuiltinTool, error) {
	spec, err := powershellSpec(cwd, options)
	if err != nil {
		return BuiltinTool{}, err
	}
	return spec.tool()
}

// CreateCodingTools is Pi's createCodingTools(cwd, options): read, bash, edit and write.
func CreateCodingTools(cwd string, options *ToolsOptions) ([]BuiltinTool, error) {
	if options == nil {
		options = &ToolsOptions{}
	}
	read, err := CreateReadTool(cwd, options.Read)
	if err != nil {
		return nil, err
	}
	bash, err := CreateBashTool(cwd, options.Bash)
	if err != nil {
		return nil, err
	}
	edit, err := CreateEditTool(cwd, options.Edit)
	if err != nil {
		return nil, err
	}
	write, err := CreateWriteTool(cwd, options.Write)
	if err != nil {
		return nil, err
	}
	return []BuiltinTool{read, bash, edit, write}, nil
}

// CreateReadOnlyTools is Pi's createReadOnlyTools(cwd, options): read, grep, find and ls.
func CreateReadOnlyTools(cwd string, options *ToolsOptions) ([]BuiltinTool, error) {
	if options == nil {
		options = &ToolsOptions{}
	}
	read, err := CreateReadTool(cwd, options.Read)
	if err != nil {
		return nil, err
	}
	grep, err := CreateGrepTool(cwd, options.Grep)
	if err != nil {
		return nil, err
	}
	find, err := CreateFindTool(cwd, options.Find)
	if err != nil {
		return nil, err
	}
	ls, err := CreateLsTool(cwd, options.Ls)
	if err != nil {
		return nil, err
	}
	return []BuiltinTool{read, grep, find, ls}, nil
}
