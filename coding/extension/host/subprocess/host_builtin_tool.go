package subprocess

// Ports packages/coding-agent/src/core/tools/{read,write,edit,bash,powershell,grep,find,ls}.ts (create<Tool>ToolDefinition, createLocalBashOperations,
// createLocalPowerShellOperations) for an extension that runs in another process.

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

// builtinToolBinDir is the managed tools directory the built-in tools resolve rg and fd in and put on PATH (Pi's process-wide getBinDir()): <agentDir>/bin,
// empty (PATH only, no downloads) when the host knows no agent directory.
func (h *Host) builtinToolBinDir() string {
	if h.hostInfo.AgentDir == "" {
		return ""
	}
	return filepath.Join(h.hostInfo.AgentDir, "bin")
}

// BuiltinToolDefinitionJSON is CallBuiltinToolDefinition over JSON: the reply for the arguments. The handler and the fused extensions' SDK hook share it.
func BuiltinToolDefinitionJSON(argsJSON []byte) ([]byte, error) {
	var args BuiltinToolDefinitionArgs
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return nil, fmt.Errorf("decode %s: %w", CallBuiltinToolDefinition, err)
	}
	tool, err := newBuiltinTool(args.Name, args.Cwd, args.Options, "", nil)
	if err != nil {
		return nil, err
	}
	definition, err := tools.BuiltinToolDefinition(tool)
	if err != nil {
		return nil, err
	}
	result := BuiltinToolDefinitionResult{
		Name: definition.Name, Label: definition.Label, Description: definition.Description, PromptSnippet: definition.PromptSnippet,
		PromptGuidelines: definition.PromptGuidelines, Parameters: definition.Parameters, OutputSchema: definition.OutputSchema,
		ConstrainedSampling: definition.ConstrainedSampling, ExecutionMode: string(definition.ExecutionMode),
	}
	if definition.RenderShell == extension.ToolRenderShellSelf {
		result.RenderShell = string(extension.ToolRenderShellSelf)
	}
	return json.Marshal(result)
}

// handleBuiltinToolDefinition serves CallBuiltinToolDefinition. The definition does not depend on the managed tools directory.
func (h *Host) handleBuiltinToolDefinition(call *CallPayload) (*CallResultPayload, error) {
	data, err := BuiltinToolDefinitionJSON(call.Args)
	if err != nil {
		return nil, err
	}
	return &CallResultPayload{Result: data}, nil
}

// builtinCallbacks are the callbacks of one CallBuiltinToolExecute, which stay in the extension. A callback runs under the call's context.
type builtinCallbacks struct {
	conn   *Conn
	handle string
	ctx    context.Context
}

// call sends one RequestBuiltinToolCallback and returns the callback's result. An error answer is the callback's throw.
func (c *builtinCallbacks) call(member string, args any) (json.RawMessage, error) {
	encoded, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(BuiltinToolCallback{Handle: c.handle, Member: member, Args: encoded})
	if err != nil {
		return nil, err
	}
	resp, err := c.conn.Request(c.ctx, &Envelope{Type: MsgRequest, Request: &RequestPayload{Method: RequestBuiltinToolCallback, Args: payload}})
	if err != nil {
		return nil, err
	}
	if resp.Response != nil && resp.Response.Error != nil {
		return nil, errors.New(resp.Response.Error.Message)
	}
	if resp.Response == nil {
		return nil, nil
	}
	return resp.Response.Result, nil
}

// callbackFailure is the panic a callback that cannot return an error (Pi's spawnHook is synchronous and throws) raises inside the tool, which the call
// recovers as the tool's thrown error.
type callbackFailure struct{ err error }

// callResult is an extension callback's result when it answered, and its error otherwise. An empty result is the zero value of the type the caller decodes.
func callResult(raw json.RawMessage, err error) (json.RawMessage, error) {
	if err != nil {
		return nil, err
	}
	return raw, nil
}

func decodeCallbackResult(err error) error {
	return fmt.Errorf("decode callback result: %w", err)
}

func callString(raw json.RawMessage, err error) (string, error) {
	var out string
	if raw, err = callResult(raw, err); err != nil || len(raw) == 0 {
		return out, err
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, decodeCallbackResult(err)
	}
	return out, nil
}

func callBool(raw json.RawMessage, err error) (bool, error) {
	var out bool
	if raw, err = callResult(raw, err); err != nil || len(raw) == 0 {
		return out, err
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, decodeCallbackResult(err)
	}
	return out, nil
}

func callStrings(raw json.RawMessage, err error) ([]string, error) {
	var out []string
	if raw, err = callResult(raw, err); err != nil || len(raw) == 0 {
		return out, err
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, decodeCallbackResult(err)
	}
	return out, nil
}

type statWire struct {
	IsDirectory bool `json:"isDirectory"`
}

func callStat(raw json.RawMessage, err error) (statWire, error) {
	var out statWire
	if raw, err = callResult(raw, err); err != nil || len(raw) == 0 {
		return out, err
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, decodeCallbackResult(err)
	}
	return out, nil
}

func callSpawnContext(raw json.RawMessage, err error) (spawnContextWire, error) {
	var out spawnContextWire
	if raw, err = callResult(raw, err); err != nil || len(raw) == 0 {
		return out, err
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, decodeCallbackResult(err)
	}
	return out, nil
}

type pathArgs struct {
	Path string `json:"path"`
}

type writeArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// The operations builders turn the members the extension announced into the tool's operations. Pi's `operations` objects are complete (each required member is
// a required property of the TypeScript interface), so the SDKs refuse a partial one before the call; an optional member the extension left out is Pi's
// undefined: read treats every file as text and grep streams the file.

func (c *builtinCallbacks) readFile(path string) ([]byte, error) {
	encoded, err := callString(c.call("operations.readFile", pathArgs{path}))
	if err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(encoded)
}

func (c *builtinCallbacks) writeFile(path, content string) error {
	_, err := c.call("operations.writeFile", writeArgs{path, content})
	return err
}

func (c *builtinCallbacks) access(path string) error {
	_, err := c.call("operations.access", pathArgs{path})
	return err
}

func (c *builtinCallbacks) exists(path string) (bool, error) {
	return callBool(c.call("operations.exists", pathArgs{path}))
}

func (c *builtinCallbacks) readOperations(members []string) *tools.ReadOperations {
	ops := &tools.ReadOperations{ReadFile: c.readFile, Access: c.access}
	if slices.Contains(members, "detectImageMimeType") {
		ops.DetectImageMimeType = func(path string) (string, error) {
			return callString(c.call("operations.detectImageMimeType", pathArgs{path}))
		}
	}
	return ops
}

func (c *builtinCallbacks) writeOperations() *tools.WriteOperations {
	return &tools.WriteOperations{
		WriteFile: c.writeFile,
		Mkdir: func(path string) error {
			_, err := c.call("operations.mkdir", pathArgs{path})
			return err
		},
	}
}

func (c *builtinCallbacks) editOperations() *tools.EditOperations {
	return &tools.EditOperations{ReadFile: c.readFile, WriteFile: c.writeFile, Access: c.access}
}

func (c *builtinCallbacks) grepOperations(members []string) *tools.GrepOperations {
	ops := &tools.GrepOperations{IsDirectory: func(path string) (bool, error) {
		return callBool(c.call("operations.isDirectory", pathArgs{path}))
	}}
	if slices.Contains(members, "readFile") {
		ops.ReadFile = func(path string) (string, error) {
			return callString(c.call("operations.readFile", pathArgs{path}))
		}
	}
	return ops
}

type findGlobArgs struct {
	Pattern string   `json:"pattern"`
	Cwd     string   `json:"cwd"`
	Ignore  []string `json:"ignore"`
	Limit   float64  `json:"limit"`
}

func (c *builtinCallbacks) findOperations() *tools.FindOperations {
	return &tools.FindOperations{
		Exists: c.exists,
		Glob: func(pattern, cwd string, options tools.FindGlobOptions) ([]string, error) {
			ignore := options.Ignore
			if ignore == nil {
				ignore = []string{}
			}
			return callStrings(c.call("operations.glob", findGlobArgs{pattern, cwd, ignore, options.Limit}))
		},
	}
}

type remoteLsStat struct{ directory bool }

func (s remoteLsStat) IsDirectory() bool { return s.directory }

func (c *builtinCallbacks) lsOperations() *tools.LsOperations {
	return &tools.LsOperations{
		Exists: c.exists,
		Stat: func(path string) (tools.LsStat, error) {
			stat, err := callStat(c.call("operations.stat", pathArgs{path}))
			return remoteLsStat{stat.IsDirectory}, err
		},
		Readdir: func(path string) ([]string, error) {
			return callStrings(c.call("operations.readdir", pathArgs{path}))
		},
	}
}

type spawnContextWire struct {
	Command string            `json:"command"`
	Cwd     string            `json:"cwd"`
	Env     map[string]string `json:"env"`
}

// spawnHook is the extension's spawnHook. Pi's hook is synchronous and a throw rejects the tool call, so a failure is a panic that the call recovers.
func (c *builtinCallbacks) spawnHook() tools.BashSpawnHook {
	return func(spawn tools.BashSpawnContext) tools.BashSpawnContext {
		env := make(map[string]string, len(spawn.Env))
		for _, entry := range spawn.Env {
			if name, value, ok := cutEnv(entry); ok {
				env[name] = value
			}
		}
		out, err := callSpawnContext(c.call("spawnHook", spawnContextWire{Command: spawn.Command, Cwd: spawn.CWD, Env: env}))
		if err != nil {
			panic(callbackFailure{err})
		}
		hooked := tools.BashSpawnContext{Command: out.Command, CWD: out.Cwd}
		names := make([]string, 0, len(out.Env))
		for name := range out.Env {
			names = append(names, name)
		}
		slices.Sort(names)
		for _, name := range names {
			hooked.Env = append(hooked.Env, name+"="+out.Env[name])
		}
		return hooked
	}
}

func cutEnv(entry string) (name, value string, ok bool) {
	for i := 1; i < len(entry); i++ { // a leading '=' belongs to the name (Windows "=C:" entries)
		if entry[i] == '=' {
			return entry[:i], entry[i+1:], true
		}
	}
	return "", "", false
}

// newBuiltinTool is create<Tool>Tool(cwd, options): the built-in tool name with the options that crossed the wire. callbacks serves the functions the
// options announce, and is nil when none can be called (a definition read).
func newBuiltinTool(name, cwd string, options *BuiltinToolOptions, binDir string, callbacks *builtinCallbacks) (agent.AgentTool, error) {
	if options == nil {
		options = &BuiltinToolOptions{}
	}
	set := &tools.ToolsOptions{BinDir: binDir}
	supplied := options.Operations
	switch name {
	case "read":
		set.Read = &tools.ReadToolOptions{AutoResizeImages: options.AutoResizeImages, ResizeOptions: options.ResizeOptions}
		if supplied != nil && callbacks != nil {
			set.Read.Operations = callbacks.readOperations(supplied)
		}
	case "write":
		if supplied != nil && callbacks != nil {
			set.Write = &tools.WriteToolOptions{Operations: callbacks.writeOperations()}
		}
	case "edit":
		if supplied != nil && callbacks != nil {
			set.Edit = &tools.EditToolOptions{Operations: callbacks.editOperations()}
		}
	case "grep":
		if supplied != nil && callbacks != nil {
			set.Grep = &tools.GrepToolOptions{Operations: callbacks.grepOperations(supplied)}
		}
	case "find":
		if supplied != nil && callbacks != nil {
			set.Find = &tools.FindToolOptions{Operations: callbacks.findOperations()}
		}
	case "ls":
		if supplied != nil && callbacks != nil {
			set.Ls = &tools.LsToolOptions{Operations: callbacks.lsOperations()}
		}
	case "bash":
		bash := &tools.BashToolOptions{CommandPrefix: options.CommandPrefix, ShellPath: options.ShellPath, ExposeSessionEnvironment: options.ExposeSessionEnvironment}
		if callbacks != nil {
			if slices.Contains(supplied, "exec") {
				bash.Operations = &remoteBashOperations{conn: callbacks.conn, handle: callbacks.handle}
			}
			if options.SpawnHook {
				bash.SpawnHook = callbacks.spawnHook()
			}
		}
		set.Bash = bash
	case "powershell":
		powershell := &tools.PowerShellToolOptions{ExposeSessionEnvironment: options.ExposeSessionEnvironment}
		if callbacks != nil {
			if slices.Contains(supplied, "exec") {
				powershell.Operations = &remoteBashOperations{conn: callbacks.conn, handle: callbacks.handle}
			}
			if options.SpawnHook {
				powershell.SpawnHook = callbacks.spawnHook()
			}
		}
		set.Powershell = powershell
	default:
		return nil, fmt.Errorf("Unknown tool name: %s", name)
	}
	return tools.CreateTool(name, cwd, set)
}

// builtinToolContext attaches what the extension's ExtensionContext gives a built-in tool: the session's working directory, and its model, session and
// reasoning level (Pi's `ctx.cwd`, `ctx.model`, `ctx.sessionManager`, `ctx.thinkingLevel`), read from the host's session callbacks.
func (h *Host) builtinToolContext(ctx context.Context) context.Context {
	var env agent.ToolEnvironment
	if h.uiBridge != nil {
		if actions := h.uiBridge.hostCallbacks(); actions != nil {
			if actions.GetSessionID != nil {
				env.SessionID = actions.GetSessionID()
			}
			if actions.GetSessionFile != nil {
				env.SessionFile = actions.GetSessionFile()
			}
			if actions.GetThinkingLevel != nil {
				env.ThinkingLevel = actions.GetThinkingLevel()
			}
			if actions.GetModelInfo != nil {
				if info := actions.GetModelInfo(); len(info) > 0 {
					var model struct {
						Provider    string               `json:"provider"`
						ID          string               `json:"id"`
						Input       []string             `json:"input"`
						InputLimits *ai.ModelInputLimits `json:"inputLimits"`
					}
					if json.Unmarshal(info, &model) == nil && model.ID != "" {
						env.Provider, env.Model, env.InputLimits = model.Provider, model.ID, model.InputLimits
						supportsImages := slices.Contains(model.Input, "image")
						env.SupportsImages = &supportsImages
					}
				}
			}
		}
	}
	ctx = agent.WithToolEnvironment(ctx, env)
	return extension.WithContext(ctx, extension.NewContext(h.cwd, extension.NoopUIContext, func() error { return nil }, extension.ContextActions{}))
}

// handleBuiltinToolExecute serves CallBuiltinToolExecute: `execute` of a built-in tool's definition. The tool runs under the call's context, which ends with
// the calling request when the extension forwarded its signal, and under the extension's own cancel. Partial results reach the extension as requests, in
// order, before the result.
func (h *Host) handleBuiltinToolExecute(ctx context.Context, conn *Conn, call *CallPayload) (result *CallResultPayload, failure error) {
	var args BuiltinToolExecuteArgs
	if err := json.Unmarshal(call.Args, &args); err != nil {
		return nil, fmt.Errorf("decode %s: %w", call.Method, err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if signal := h.nested.signal(conn, args.ExecuteID); signal != nil {
		stop := context.AfterFunc(signal, cancel)
		defer stop()
	}
	// The extension's next call, including the cancel, applies after this call started.
	extension.CallInitiated(ctx)
	if args.Aborted {
		cancel()
	}
	if args.Context {
		runCtx = h.builtinToolContext(runCtx)
	}
	var callbacks *builtinCallbacks
	if args.Callbacks != "" {
		callbacks = &builtinCallbacks{conn: conn, handle: args.Callbacks, ctx: runCtx}
	}
	tool, err := newBuiltinTool(args.Name, args.Cwd, args.Options, h.builtinToolBinDir(), callbacks)
	if err != nil {
		return nil, err
	}
	// A hook that throws is the tool's throw.
	defer func() {
		if recovered := recover(); recovered != nil {
			cause, ok := recovered.(callbackFailure)
			if !ok {
				panic(recovered)
			}
			result, failure = nil, cause.err
		}
	}()

	var open sync.Mutex
	finished := false
	var updateErr error
	var onUpdate agent.ToolUpdateCallback
	if args.WantsUpdates {
		// The update waits for the extension's callback, as Pi's runs in the tool's tick. The request outlives the call's cancellation: Pi's callback has none.
		updateCtx := context.WithoutCancel(runCtx)
		onUpdate = func(partial agent.AgentToolResult) {
			open.Lock()
			defer open.Unlock()
			if finished {
				return
			}
			payload, err := json.Marshal(ExecuteToolUpdate{ExecuteID: args.ExecuteID, Result: agentToolResultWire(partial)})
			if err == nil {
				var resp *Envelope
				if resp, err = conn.Request(updateCtx, &Envelope{Type: MsgRequest, Request: &RequestPayload{Method: RequestExecuteToolUpdate, Args: payload}}); err == nil && resp.Response != nil && resp.Response.Error != nil {
					err = errors.New(resp.Response.Error.Message)
				}
			}
			if err != nil && updateErr == nil {
				updateErr = err
			}
		}
	}
	toolResult, err := tool.Execute(runCtx, args.ToolCallID, args.Params, onUpdate)
	open.Lock()
	finished = true
	open.Unlock()
	if err != nil {
		return nil, err
	}
	if updateErr != nil {
		// Pi's callback throws inside the tool's execute, which rejects with it.
		return nil, updateErr
	}
	if toolResult.Thrown {
		// upstream: the tool throws; the Go tools return the thrown message as a result the agent loop turns into an error result.
		return nil, errors.New(textOf(toolResult))
	}
	return &CallResultPayload{Result: agentToolResultWire(toolResult)}, nil
}

func textOf(result agent.AgentToolResult) string {
	var text strings.Builder
	for _, block := range result.Content {
		if t, ok := block.(ai.TextContent); ok {
			if text.Len() > 0 {
				text.WriteByte('\n')
			}
			text.WriteString(t.Text)
		}
	}
	return text.String()
}

// handleBuiltinToolLocalExec serves CallBuiltinToolLocalExec: `exec` of createLocalBashOperations / createLocalPowerShellOperations.
func (h *Host) handleBuiltinToolLocalExec(ctx context.Context, conn *Conn, call *CallPayload) (*CallResultPayload, error) {
	var args BuiltinToolLocalExecArgs
	if err := json.Unmarshal(call.Args, &args); err != nil {
		return nil, fmt.Errorf("decode %s: %w", call.Method, err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if signal := h.nested.signal(conn, args.ExecuteID); signal != nil {
		stop := context.AfterFunc(signal, cancel)
		defer stop()
	}
	extension.CallInitiated(ctx)
	if args.Aborted {
		cancel()
	}
	var operations tools.BashOperations
	switch args.Shell {
	case "bash":
		local := tools.CreateLocalBashOperations(&tools.LocalBashOptions{ShellPath: args.ShellPath, BinDir: h.builtinToolBinDir()})
		operations = local
	case "powershell":
		local := tools.NewLocalPowerShellOperations()
		local.BinDir = h.builtinToolBinDir()
		operations = local
	default:
		return nil, fmt.Errorf("unknown shell %q", args.Shell)
	}
	options := tools.BashOperationsExecOptions{}
	if args.Timeout != nil {
		options.Timeout = args.Timeout
	}
	if args.Env != nil {
		names := make([]string, 0, len(*args.Env))
		for name := range *args.Env {
			names = append(names, name)
		}
		slices.Sort(names)
		options.Env = []string{}
		for _, name := range names {
			options.Env = append(options.Env, name+"="+(*args.Env)[name])
		}
	}
	// Output chunks reach the extension in order and before the reply. A failed delivery ends the command: nobody can read the rest.
	var deliveryErr error
	options.OnData = func(data []byte) {
		if deliveryErr != nil {
			return
		}
		payload, err := json.Marshal(BuiltinToolExecData{ExecuteID: args.ExecuteID, Data: base64.StdEncoding.EncodeToString(data)})
		if err == nil {
			var resp *Envelope
			if resp, err = conn.Request(context.WithoutCancel(runCtx), &Envelope{Type: MsgRequest, Request: &RequestPayload{Method: RequestBuiltinToolExecData, Args: payload}}); err == nil && resp.Response != nil && resp.Response.Error != nil {
				err = errors.New(resp.Response.Error.Message)
			}
		}
		if err != nil {
			deliveryErr = err
			cancel()
		}
	}
	exec, err := operations.Exec(runCtx, args.Command, args.Cwd, options)
	if deliveryErr != nil {
		return nil, deliveryErr
	}
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(BuiltinToolLocalExecResult{ExitCode: exec.ExitCode})
	return &CallResultPayload{Result: data}, err
}
