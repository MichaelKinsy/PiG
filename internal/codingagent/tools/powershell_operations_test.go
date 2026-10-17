package tools

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

type recordingPowerShellOperations struct {
	command, cwd string
}

func (o *recordingPowerShellOperations) Exec(_ context.Context, command, cwd string, opts BashOperationsExecOptions) (BashOperationsResult, error) {
	o.command, o.cwd = command, cwd
	opts.OnData([]byte("remote output\n"))
	return BashOperationsResult{ExitCode: new(0)}, nil
}

// Ports packages/coding-agent/src/core/tools/powershell.ts: PowerShellToolOptions.operations replaces
// createLocalPowerShellOperations, so the tool calls it with the command and cwd it was given.
func TestPowerShellToolCallsCustomOperations(t *testing.T) {
	ops := &recordingPowerShellOperations{}
	dir := t.TempDir()
	tool := CreatePowerShellTool(dir, &PowerShellToolOptions{Operations: ops})
	params, _ := json.Marshal(map[string]any{"command": "Get-Date"})
	result, err := tool.Execute(t.Context(), "call", params, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ops.command != "Get-Date" || ops.cwd != dir {
		t.Fatalf("operation saw command %q cwd %q", ops.command, ops.cwd)
	}
	if result.IsError || !strings.Contains(result.Text(), "remote output") {
		t.Fatalf("result = %+v", result)
	}
}

// createLocalPowerShellOperations wraps each command with the UTF-8 output switch and resolves PowerShell by name.
func TestLocalPowerShellOperationsWrapCommandsWithTheUTF8OutputSwitch(t *testing.T) {
	ops := NewLocalPowerShellOperations()
	if ops.ShellName != "PowerShell" || ops.WrapCommand("x") != powerShellUTF8OutputPrefix+"x" {
		t.Fatalf("ops = %+v, wrapped = %q", ops, ops.WrapCommand("x"))
	}
}

func TestPowerShellToolDefaultsToLocalOperations(t *testing.T) {
	if _, ok := (&PowerShellTool{}).operations().(*LocalShellOperations); !ok {
		t.Fatal("default operations are not the local PowerShell operations")
	}
}

// powershell.ts createPowerShellTool(cwd, options): options.operations reaches the tool, so a factory-built tool runs the injected operations.
// Pi: packages/coding-agent/src/core/tools/powershell.ts:30 (PowerShellToolOptions.operations).
func TestCreatePowerShellToolPassesOperationsFromOptions(t *testing.T) {
	ops := &recordingPowerShellOperations{}
	dir := t.TempDir()
	options := &PowerShellToolOptions{Operations: ops}
	tool := CreatePowerShellTool(dir, options)
	params, _ := json.Marshal(map[string]any{"command": "Get-Location"})
	if _, err := tool.Execute(t.Context(), "call", params, nil); err != nil {
		t.Fatal(err)
	}
	if ops.command != "Get-Location" || ops.cwd != dir {
		t.Fatalf("factory dropped options.Operations: command %q cwd %q", ops.command, ops.cwd)
	}
}

// powershell.ts createLocalPowerShellOperations resolves the shell with getPowerShellConfig at each run: the operations' resolver answers exactly what GetPowerShellConfig does on this host
// (the Windows-only error elsewhere), so a local PowerShell tool never runs through another shell.
func TestLocalPowerShellOperationsResolveTheShellWithGetPowerShellConfig(t *testing.T) {
	ops := NewLocalPowerShellOperations()
	if ops.ResolveShell == nil {
		t.Fatal("the local PowerShell operations have no shell resolver")
	}
	// The resolver is GetPowerShellConfig itself. Calling both and comparing their answers ran `where pwsh.exe` twice,
	// and on a loaded Windows runner one lookup could hit its 5 s timeout (upstream's spawnSync timeout) and fall back to
	// powershell.exe while the other found pwsh.exe.
	if reflect.ValueOf(ops.ResolveShell).Pointer() != reflect.ValueOf(GetPowerShellConfig).Pointer() {
		t.Fatal("the local PowerShell operations resolve the shell with something other than GetPowerShellConfig")
	}
}
