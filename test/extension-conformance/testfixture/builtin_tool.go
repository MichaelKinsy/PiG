package testfixture

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/extensions/sdk"
	jsjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// builtinProbeCase is one case of builtin_probe (TestConformance_BuiltinToolDelegationAcrossSDKs). Every SDK's fixture implements the same cases, so the
// test compares what each reports with Pi's own code, which the Node fixture runs.
type builtinProbeCase struct {
	// Kind is "cwd" (report ctx.cwd), "register" (override read and bash with wrapped built-ins), "definition", "delegate" or "local".
	Kind       string          `json:"kind"`
	Tool       string          `json:"tool"`
	FactoryCwd string          `json:"factoryCwd"`
	Params     json.RawMessage `json:"params"`
	// NoContext is createXTool(...).execute(id, params): the tool without an extension context.
	NoContext bool `json:"noContext"`
	// Updates collects the partial results the tool streams.
	Updates bool `json:"updates"`
	// AbortOnOutput aborts the call's signal when the tool first reports output, so the abort cannot come before the command started; PreAborted passes a signal
	// that is aborted already.
	AbortOnOutput bool `json:"abortOnOutput"`
	PreAborted    bool `json:"preAborted"`
	// Ops names the operations bundle, Hook the spawn hook.
	Ops                      string `json:"ops"`
	Hook                     bool   `json:"hook"`
	CommandPrefix            string `json:"commandPrefix"`
	ExposeSessionEnvironment *bool  `json:"exposeSessionEnvironment"`
	AutoResizeImages         *bool  `json:"autoResizeImages"`
	// Shell, Command, Timeout, Env and AbortAfterMs are the local operations case.
	Shell   string            `json:"shell"`
	Command string            `json:"command"`
	Timeout *float64          `json:"timeout"`
	Env     map[string]string `json:"env"`
}

type builtinCallLog struct {
	mu    sync.Mutex
	calls []string
}

func (l *builtinCallLog) add(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, fmt.Sprintf(format, args...))
}

// builtinResultReport is the part of a tool result the probe reports.
func builtinResultReport(r sdk.AgentToolResult) map[string]any {
	blocks := make([]map[string]any, 0, len(r.Content))
	for _, block := range r.Content {
		entry := map[string]any{"type": block.Type}
		if block.Type == "text" {
			entry["text"] = block.Text
		} else {
			entry["mimeType"] = block.MimeType
		}
		blocks = append(blocks, entry)
	}
	report := map[string]any{"content": blocks, "isError": r.IsError}
	if r.Details != nil {
		report["details"] = r.Details
		// The text of the details, in member order: a map would sort it.
		text, err := jsjson.MarshalJS(r.Details)
		if err != nil {
			panic(err)
		}
		report["detailsJSON"] = string(text)
	}
	if structured, ok := r.StructuredContent.(*sdk.OrderedObject); ok {
		report["structuredKeys"] = structured.Keys()
		code, _ := structured.Get("exit_code")
		report["exitCode"] = code
	}
	return report
}

// builtinProbeOptions builds Pi's options object for the case, with callbacks that log into log.
func builtinProbeSpec(c builtinProbeCase, log *builtinCallLog) (definition func() (sdk.BuiltinToolDefinition, error), tool func() (sdk.BuiltinTool, error)) {
	cwd := c.FactoryCwd
	hook := sdk.BashSpawnHook(nil)
	if c.Hook {
		hook = func(spawn sdk.BashSpawnContext) (sdk.BashSpawnContext, error) {
			log.add("spawnHook:%s", spawn.Command)
			env := map[string]string{}
			maps.Copy(env, spawn.Env)
			env["HOOK_VAR"] = "hook-env"
			return sdk.BashSpawnContext{Command: "printf '%s|%s' \"$HOOK_VAR\" \"$PI_SESSION_ID\"", Cwd: spawn.Cwd, Env: env}, nil
		}
	}
	switch c.Tool {
	case "read":
		options := &sdk.ReadToolOptions{AutoResizeImages: c.AutoResizeImages}
		if c.Ops == "custom" {
			options.Operations = &sdk.ReadOperations{
				ReadFile: func(path string) ([]byte, error) { log.add("readFile:%s", path); return []byte("OPS:" + path), nil },
				Access:   func(path string) error { log.add("access:%s", path); return nil },
			}
		}
		return func() (sdk.BuiltinToolDefinition, error) { return sdk.CreateReadToolDefinition(cwd, options) },
			func() (sdk.BuiltinTool, error) { return sdk.CreateReadTool(cwd, options) }
	case "write":
		options := &sdk.WriteToolOptions{}
		if c.Ops == "custom" {
			options.Operations = &sdk.WriteOperations{
				WriteFile: func(path, content string) error { log.add("writeFile:%s:%s", path, content); return nil },
				Mkdir:     func(dir string) error { log.add("mkdir:%s", dir); return nil },
			}
		}
		return func() (sdk.BuiltinToolDefinition, error) { return sdk.CreateWriteToolDefinition(cwd, options) },
			func() (sdk.BuiltinTool, error) { return sdk.CreateWriteTool(cwd, options) }
	case "edit":
		options := &sdk.EditToolOptions{}
		if c.Ops == "custom" {
			options.Operations = &sdk.EditOperations{
				ReadFile:  func(path string) ([]byte, error) { log.add("readFile:%s", path); return []byte("hello world\n"), nil },
				WriteFile: func(path, content string) error { log.add("writeFile:%s:%s", path, content); return nil },
				Access:    func(path string) error { log.add("access:%s", path); return nil },
			}
		}
		return func() (sdk.BuiltinToolDefinition, error) { return sdk.CreateEditToolDefinition(cwd, options) },
			func() (sdk.BuiltinTool, error) { return sdk.CreateEditTool(cwd, options) }
	case "ls":
		options := &sdk.LsToolOptions{}
		if c.Ops == "custom" {
			options.Operations = &sdk.LsOperations{
				Exists: func(path string) (bool, error) { log.add("exists:%s", path); return true, nil },
				Stat: func(path string) (sdk.LsStat, error) {
					log.add("stat:%s", path)
					return sdk.LsStat{IsDirectory: !strings.Contains(path, ".")}, nil
				},
				Readdir: func(path string) ([]string, error) {
					log.add("readdir:%s", path)
					return []string{"b.txt", "a.txt", "sub"}, nil
				},
			}
		}
		return func() (sdk.BuiltinToolDefinition, error) { return sdk.CreateLsToolDefinition(cwd, options) },
			func() (sdk.BuiltinTool, error) { return sdk.CreateLsTool(cwd, options) }
	case "grep":
		options := &sdk.GrepToolOptions{}
		if c.Ops == "custom" {
			options.Operations = &sdk.GrepOperations{IsDirectory: func(path string) (bool, error) {
				log.add("isDirectory:%s", path)
				return false, errors.New("no such path for the probe")
			}}
		}
		return func() (sdk.BuiltinToolDefinition, error) { return sdk.CreateGrepToolDefinition(cwd, options) },
			func() (sdk.BuiltinTool, error) { return sdk.CreateGrepTool(cwd, options) }
	case "find":
		options := &sdk.FindToolOptions{}
		if c.Ops == "custom" {
			options.Operations = &sdk.FindOperations{
				Exists: func(path string) (bool, error) { log.add("exists:%s", path); return false, nil },
				Glob: func(pattern, cwd string, o sdk.FindGlobOptions) ([]string, error) {
					log.add("glob:%s", pattern)
					return nil, nil
				},
			}
		}
		return func() (sdk.BuiltinToolDefinition, error) { return sdk.CreateFindToolDefinition(cwd, options) },
			func() (sdk.BuiltinTool, error) { return sdk.CreateFindTool(cwd, options) }
	case "powershell":
		options := &sdk.PowerShellToolOptions{ExposeSessionEnvironment: c.ExposeSessionEnvironment}
		return func() (sdk.BuiltinToolDefinition, error) { return sdk.CreatePowerShellToolDefinition(cwd, options) },
			func() (sdk.BuiltinTool, error) { return sdk.CreatePowerShellTool(cwd, options) }
	default: // bash
		options := &sdk.BashToolOptions{CommandPrefix: c.CommandPrefix, ExposeSessionEnvironment: c.ExposeSessionEnvironment, SpawnHook: hook}
		if c.Ops == "custom" {
			options.Operations = &sdk.BashOperations{Exec: func(command, cwd string, o sdk.BashExecOptions) (sdk.BashExecResult, error) {
				log.add("exec:%s|cwd=%s", command, cwd)
				o.OnData([]byte("custom:" + command))
				return sdk.BashExecResult{ExitCode: new(0)}, nil
			}}
		}
		return func() (sdk.BuiltinToolDefinition, error) { return sdk.CreateBashToolDefinition(cwd, options) },
			func() (sdk.BuiltinTool, error) { return sdk.CreateBashTool(cwd, options) }
	}
}

// registerBuiltinToolProbe adds builtin_probe: the cases of TestConformance_BuiltinToolDelegationAcrossSDKs, reported as `builtin:<json>:end`.
func registerBuiltinToolProbe(ext *sdk.Extension) {
	ext.Command("builtin_probe", "Delegate to a built-in tool through the SDK and report what it returned", func(ctx sdk.Context, args string) error {
		var c builtinProbeCase
		if err := json.Unmarshal([]byte(args), &c); err != nil {
			return err
		}
		report, err := builtinProbe(ctx, c)
		if err != nil {
			return err
		}
		wire, err := json.Marshal(report)
		if err != nil {
			return err
		}
		ctx.Notify("builtin:"+string(wire)+":end", "info")
		return nil
	})
}

func builtinProbe(ctx sdk.Context, c builtinProbeCase) (map[string]any, error) {
	switch c.Kind {
	case "cwd":
		return map[string]any{"cwd": ctx.Cwd()}, nil
	case "register":
		return map[string]any{"registered": registerDelegatingOverrides(ctx, c.FactoryCwd)}, nil
	case "local":
		return builtinLocalProbe(c)
	case "sets":
		coding, err := sdk.CreateCodingTools(c.FactoryCwd, nil)
		if err != nil {
			return nil, err
		}
		readOnly, err := sdk.CreateReadOnlyTools(c.FactoryCwd, nil)
		if err != nil {
			return nil, err
		}
		names := func(tools []sdk.BuiltinTool) []string {
			out := []string{}
			for _, tool := range tools {
				out = append(out, tool.Name)
			}
			return out
		}
		return map[string]any{"coding": names(coding), "readOnly": names(readOnly)}, nil
	}
	log := &builtinCallLog{calls: []string{}}
	definition, tool := builtinProbeSpec(c, log)
	report := map[string]any{}
	var params map[string]any
	if len(c.Params) > 0 {
		if err := json.Unmarshal(c.Params, &params); err != nil {
			return nil, err
		}
	}
	if c.Kind == "definition" {
		def, err := definition()
		if err != nil {
			return nil, err
		}
		report["definition"] = map[string]any{
			"name": def.Name, "label": def.Label, "description": def.Description, "promptSnippet": def.PromptSnippet, "promptGuidelines": def.PromptGuidelines,
			"parameters": def.Parameters, "outputSchema": def.OutputSchema, "constrainedSampling": def.ConstrainedSampling, "executionMode": def.ExecutionMode,
			"renderShell": string(def.RenderShell),
		}
		return report, nil
	}

	signal, cancel := context.WithCancel(context.Background())
	defer cancel()
	if c.PreAborted {
		cancel()
	}
	updates := []string{}
	var updatesMu sync.Mutex
	var onUpdate func(sdk.AgentToolResult)
	if c.Updates || c.AbortOnOutput {
		onUpdate = func(partial sdk.AgentToolResult) {
			updatesMu.Lock()
			updates = append(updates, partial.Text())
			updatesMu.Unlock()
			if c.AbortOnOutput && partial.Text() != "" {
				cancel()
			}
		}
	}
	var result sdk.AgentToolResult
	var err error
	if c.NoContext {
		t, terr := tool()
		if terr != nil {
			return nil, terr
		}
		result, err = t.Execute("probe-call", params, signal, onUpdate)
	} else {
		def, derr := definition()
		if derr != nil {
			return nil, derr
		}
		result, err = def.ExecuteWith(ctx, "probe-call", params, signal, onUpdate)
	}
	if err != nil {
		report["error"] = err.Error()
	} else {
		report["result"] = builtinResultReport(result)
	}
	if c.Updates {
		updatesMu.Lock()
		report["updates"] = updates
		updatesMu.Unlock()
	}
	log.mu.Lock()
	report["calls"] = log.calls
	log.mu.Unlock()
	return report, nil
}

func builtinLocalProbe(c builtinProbeCase) (map[string]any, error) {
	var operations sdk.BashOperations
	if c.Shell == "powershell" {
		operations = sdk.CreateLocalPowerShellOperations()
	} else {
		operations = sdk.CreateLocalBashOperations(nil)
	}
	signal, cancel := context.WithCancel(context.Background())
	defer cancel()
	var chunks []string
	var mu sync.Mutex
	options := sdk.BashExecOptions{OnData: func(data []byte) {
		mu.Lock()
		chunks = append(chunks, string(data))
		mu.Unlock()
		if c.AbortOnOutput {
			cancel()
		}
	}, Signal: signal, Timeout: c.Timeout}
	if c.Env != nil {
		options.Env = c.Env
	}
	result, err := operations.Exec(c.Command, c.FactoryCwd, options)
	report := map[string]any{}
	mu.Lock()
	report["output"] = strings.Join(chunks, "")
	mu.Unlock()
	switch {
	case err != nil:
		report["error"] = err.Error()
	case result.ExitCode == nil:
		report["exitCode"] = nil
	default:
		report["exitCode"] = *result.ExitCode
	}
	return report, nil
}

// registerDelegatingOverrides overrides the read and bash tools with wrapped built-ins, as Pi's `pi.registerTool({ ...definition, execute })` does: the
// wrapper runs the built-in through the SDK and post-processes its result, so only a tool that really ran on the host can produce the sentinel text.
func registerDelegatingOverrides(ctx sdk.Context, factoryCwd string) []string {
	var names []string
	read, err := sdk.CreateReadToolDefinition(factoryCwd, nil)
	if err != nil {
		panic(err)
	}
	runRead := read.Execute
	read.Execute = func(ctx sdk.Context, params map[string]any) (any, error) {
		result, err := runRead(ctx, params)
		if err != nil {
			return nil, err
		}
		out := result.(sdk.AgentToolResult)
		out.Content = append([]sdk.ToolResultContent(nil), out.Content...)
		out.Content[0].Text = "«delegated:" + out.Content[0].Text + "»"
		out.Details = map[string]any{"delegated": true}
		return out, nil
	}
	ctx.RegisterTool(read.ToolDefinition)
	names = append(names, read.Name)

	bash, err := sdk.CreateBashToolDefinition(factoryCwd, nil)
	if err != nil {
		panic(err)
	}
	runBash := bash.Execute
	bash.Execute = func(ctx sdk.Context, params map[string]any) (any, error) {
		result, err := runBash(ctx, params)
		if err != nil {
			return nil, err
		}
		out := result.(sdk.AgentToolResult)
		out.Content = append([]sdk.ToolResultContent(nil), out.Content...)
		out.Content[0].Text = "«" + out.Content[0].Text + "»"
		return out, nil
	}
	ctx.RegisterTool(bash.ToolDefinition)
	return append(names, bash.Name)
}
