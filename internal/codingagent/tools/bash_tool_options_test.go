package tools

import (
	"context"
	"encoding/json"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// upstream: bash.ts createBashTool(cwd, options): the prefix is applied first, then spawnHook rewrites command, cwd and env, and operations.exec receives the rewritten context.
// Pi: packages/coding-agent/src/core/tools/bash.ts:218 (BashToolOptions.commandPrefix); packages/coding-agent/src/core/tools/powershell.ts:30 (BashToolOptions.operations); packages/coding-agent/src/core/tools/powershell.ts:30 (BashToolOptions.spawnHook).
func TestCreateBashToolAppliesPrefixThenSpawnHookToCustomOperations(t *testing.T) {
	var gotCommand, gotCwd string
	var gotEnv []string
	tool := CreateBashTool("/original", &BashToolOptions{
		CommandPrefix: "set -e",
		Operations: portBashOperations(func(_ context.Context, command, cwd string, opts BashOperationsExecOptions) (BashOperationsResult, error) {
			gotCommand, gotCwd, gotEnv = command, cwd, opts.Env
			opts.OnData([]byte("ok"))
			return BashOperationsResult{ExitCode: new(0)}, nil
		}),
		SpawnHook: func(c BashSpawnContext) BashSpawnContext {
			if c.Command != "set -e\necho hi" {
				t.Errorf("hook saw command %q, want the prefixed command", c.Command)
			}
			return BashSpawnContext{Command: c.Command + " # hooked", CWD: "/rewritten", Env: append(slices.Clone(c.Env), "HOOK_VAR=1")}
		},
	})
	ctx := context.Background()
	if _, err := tool.Execute(ctx, "id", json.RawMessage(`{"command":"echo hi"}`), nil); err != nil {
		t.Fatal(err)
	}
	if gotCommand != "set -e\necho hi # hooked" || gotCwd != "/rewritten" || !slices.Contains(gotEnv, "HOOK_VAR=1") {
		t.Fatalf("operations saw command=%q cwd=%q hookVar=%v", gotCommand, gotCwd, slices.Contains(gotEnv, "HOOK_VAR=1"))
	}
}

// upstream: exposeSessionEnvironment defaults to true; false hides the session variables and the prompt guideline that mentions them.
// Pi: packages/coding-agent/src/core/tools/powershell.ts:30 (BashToolOptions.exposeSessionEnvironment).
func TestCreateBashToolExposeSessionEnvironmentDefaultsToTrue(t *testing.T) {
	if CreateBashTool("/", &BashToolOptions{}).HideSessionEnvironment {
		t.Fatal("the session environment must be exposed by default")
	}
	if !CreateBashTool("/", &BashToolOptions{ExposeSessionEnvironment: new(false)}).HideSessionEnvironment {
		t.Fatal("ExposeSessionEnvironment=false must hide it")
	}
}

// upstream: createBashTool({shellPath}) resolves the local shell from that path; a missing path fails the command with getShellConfig's message.
// Pi: packages/coding-agent/src/core/tools/bash.ts:220 (BashToolOptions.shellPath).
func TestCreateBashToolShellPathFailsForAMissingShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("path style")
	}
	tool := CreateBashTool(t.TempDir(), &BashToolOptions{ShellPath: "/nonexistent/shell"})
	result, err := tool.Execute(context.Background(), "id", json.RawMessage(`{"command":"true"}`), nil)
	text := ""
	if err != nil {
		text = err.Error()
	}
	for _, block := range result.Content {
		text += block.(ai.TextContent).Text
	}
	if !strings.Contains(text, "Custom shell path not found: /nonexistent/shell") {
		t.Fatalf("result = %q (err %v)", text, err)
	}
}

// upstream: powershell.ts createPowerShellToolDefinition passes options.operations to the shell tool, which replaces createLocalPowerShellOperations; spawnHook still rewrites the context first.
// Pi: packages/coding-agent/src/core/tools/powershell.ts:30 (PowerShellToolOptions.operations); packages/coding-agent/src/core/tools/powershell.ts:30 (PowerShellToolOptions.spawnHook).
func TestCreatePowerShellToolRunsCustomOperationsAfterTheSpawnHook(t *testing.T) {
	var gotCommand, gotCwd string
	var ops PowerShellOperations = portBashOperations(func(_ context.Context, command, cwd string, opts BashOperationsExecOptions) (BashOperationsResult, error) {
		gotCommand, gotCwd = command, cwd
		opts.OnData([]byte("custom"))
		return BashOperationsResult{ExitCode: new(0)}, nil
	})
	tool := CreatePowerShellTool("/original", &PowerShellToolOptions{
		Operations: ops,
		SpawnHook: func(c BashSpawnContext) BashSpawnContext {
			return BashSpawnContext{Command: c.Command + " # hooked", CWD: "/rewritten", Env: c.Env}
		},
	})
	result, err := tool.Execute(context.Background(), "id", json.RawMessage(`{"command":"Get-Date"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if gotCommand != "Get-Date # hooked" || gotCwd != "/rewritten" {
		t.Fatalf("operations saw command=%q cwd=%q, want the hooked context without the UTF-8 prefix", gotCommand, gotCwd)
	}
	if text, _ := result.Content[0].(ai.TextContent); !strings.Contains(text.Text, "custom") {
		t.Fatalf("result = %+v, want the custom operations' output", result.Content)
	}
}
