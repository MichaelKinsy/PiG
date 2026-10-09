package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/env"
	envnode "github.com/MichaelKinsy/PiG/durable/env/node"
)

// Ports the "powershell" describe of packages/durable/test/tools.test.ts (1.0.4).

// programsEnv is an environment where only the listed programs exist; it records each command and prints output.
type programsEnv struct {
	*envnode.NodeExecutionEnv
	installed []string
	output    string
	exitCode  int
	mu        sync.Mutex
	commands  [][]string
}

func newProgramsEnv(t *testing.T, installed []string, output string, exitCode int) *programsEnv {
	t.Helper()
	return &programsEnv{NodeExecutionEnv: envnode.NewNodeExecutionEnv(envnode.NodeExecutionEnvOptions{Cwd: t.TempDir()}), installed: installed, output: output, exitCode: exitCode}
}

func (programs *programsEnv) Exec(ctx context.Context, command any, options *env.ShellExecOptions) (env.ShellExecResult, error) {
	argv, isArgv := command.([]string)
	programs.mu.Lock()
	programs.commands = append(programs.commands, argv)
	programs.mu.Unlock()
	if !isArgv || !slices.Contains(programs.installed, argv[0]) {
		name := "<shell>"
		if isArgv {
			name = argv[0]
		}
		return env.ShellExecResult{}, env.NewExecutionError(env.ExecutionErrorSpawnError, "spawn "+name+" ENOENT", nil)
	}
	options.OnOutput(ctx, programs.output, env.ShellOutputInfo{Stream: env.ShellStdout})
	return env.ShellExecResult{ExitCode: programs.exitCode}, nil
}

func TestPowerShellRunsTheCommandWithPwshAsOneArgumentForcingUTF8Output(t *testing.T) {
	executionEnv := newProgramsEnv(t, []string{"pwsh"}, "héllo\n", 0)
	result := mustRun(t, CreatePowerShellTool(nil), map[string]any{"command": "Write-Output 'héllo'"}, executionEnv)
	if got := strings.Join(result.output, ""); got != "héllo\n" {
		t.Fatalf("output = %q", got)
	}
	want := [][]string{{"pwsh", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command",
		"try { [Console]::OutputEncoding=[System.Text.Encoding]::UTF8 } catch {}\nWrite-Output 'héllo'"}}
	if len(executionEnv.commands) != 1 || !slices.Equal(executionEnv.commands[0], want[0]) {
		t.Fatalf("commands = %q, want %q", executionEnv.commands, want)
	}
}

// packages/durable/src/tools/bash.ts:40-43: PowerShellToolOptions.commandPrefix, prepare and programs (tried in order, a program that cannot start is skipped).
func TestPowerShellFallsBackToWindowsPowerShellAndReportsTheLastStartFailure(t *testing.T) {
	windowsOnly := newProgramsEnv(t, []string{"powershell"}, "ok", 0)
	result := mustRun(t, CreatePowerShellTool(&PowerShellToolOptions{CommandPrefix: "$x = 1"}), map[string]any{"command": "$x"}, windowsOnly)
	if got := strings.Join(result.output, ""); got != "ok" {
		t.Fatalf("output = %q", got)
	}
	var names []string
	for _, command := range windowsOnly.commands {
		names = append(names, command[0])
	}
	if !slices.Equal(names, []string{"pwsh", "powershell"}) {
		t.Fatalf("programs tried = %q", names)
	}
	if script := windowsOnly.commands[1][len(windowsOnly.commands[1])-1]; !strings.HasSuffix(script, "\n$x = 1\n$x") {
		t.Fatalf("script = %q", script)
	}

	none := newProgramsEnv(t, nil, "", 0)
	_, err := runFailing(t, CreatePowerShellTool(nil), map[string]any{"command": "1"}, none)
	if err.Error() != "spawn powershell ENOENT" {
		t.Fatalf("error = %v", err)
	}
}

// An empty program list runs nothing and fails, as upstream's runCommand throws "No command to run" when
// `options.programs` is `[]` (packages/durable/src/tools/bash.ts).
// packages/durable/src/tools/bash.ts:42-43: PowerShellToolOptions.programs lists the programs to try; none leaves nothing to run.
func TestPowerShellFailsWithoutAProgramToTry(t *testing.T) {
	executionEnv := newProgramsEnv(t, []string{"pwsh"}, "never", 0)
	_, err := runFailing(t, CreatePowerShellTool(&PowerShellToolOptions{Programs: []string{}}), map[string]any{"command": "1"}, executionEnv)
	if err == nil || err.Error() != "No command to run" {
		t.Fatalf("error = %v, want No command to run", err)
	}
	if len(executionEnv.commands) != 0 {
		t.Fatalf("commands = %q, want none", executionEnv.commands)
	}
}

func TestPowerShellThrowsOnANonzeroExitAfterStreamingTheOutput(t *testing.T) {
	executionEnv := newProgramsEnv(t, []string{"pwsh"}, "partial", 3)
	failed, err := runFailing(t, CreatePowerShellTool(nil), map[string]any{"command": "exit 3"}, executionEnv)
	if err.Error() != "Command exited with code 3" {
		t.Fatalf("error = %v", err)
	}
	if got := strings.Join(failed.output, ""); got != "partial" {
		t.Fatalf("output = %q", got)
	}
}

func TestPowerShellRunsRealPowerShellWithUTF8Output(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("upstream runs this case only on Windows (it.runIf(process.platform === \"win32\"))")
	}
	result := mustRun(t, CreatePowerShellTool(nil), map[string]any{"command": "Write-Output ('h' + [char]0xe9 + 'llo'); exit 0"}, createEnv(t))
	if got := strings.TrimSpace(strings.Join(result.output, "")); got != "héllo" {
		t.Fatalf("output = %q", got)
	}
}

// growingReadEnv appends to the log on every read, as a busy writer would.
type growingReadEnv struct {
	*envnode.NodeExecutionEnv
	logPath string
}

type growingReader struct {
	env.BinaryReader
	logPath string
}

func (reader growingReader) Read(ctx context.Context, position, length int64) ([]byte, error) {
	file, err := os.OpenFile(reader.logPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err == nil {
		_, _ = file.WriteString("more\n")
		_ = file.Close()
	}
	return reader.BinaryReader.Read(ctx, position, length)
}

func (growing *growingReadEnv) OpenBinaryReader(ctx context.Context, path string, options *env.OpenBinaryReaderOptions) (env.BinaryReader, error) {
	reader, err := growing.NodeExecutionEnv.OpenBinaryReader(ctx, path, options)
	if err != nil {
		return nil, err
	}
	return growingReader{BinaryReader: reader, logPath: growing.logPath}, nil
}

// upstream: packages/durable/test/tools.test.ts "reads a log that grows while it is read" (1.0.4)
func TestReadReadsALogThatGrowsWhileItIsRead(t *testing.T) {
	base := createEnv(t)
	writeText(t, base, "app.log", "one\ntwo\n")
	growing := &growingReadEnv{NodeExecutionEnv: base, logPath: filepath.Join(base.Cwd(), "app.log")}
	result := mustRun(t, CreateReadTool(), map[string]any{"path": "app.log", "limit": 2}, growing)
	if got := textOutput(result.ToolExecutionResult); got != "one\ntwo" {
		t.Fatalf("read = %q, want the scanned lines", got)
	}
}

// packages/durable/src/tools/bash.ts:23-35 (PowerShellToolOptions.prepare): the hook sees the command after the prefix,
// its change reaches the program's script argument, and its error fails the call before any program starts.
func TestPowerShellPrepareChangesTheScriptAndItsErrorFailsTheCall(t *testing.T) {
	executionEnv := newProgramsEnv(t, []string{"pwsh"}, "ok", 0)
	var seen string
	tool := CreatePowerShellTool(&PowerShellToolOptions{CommandPrefix: "$p = 1", Prepare: func(_ context.Context, execution *BashExecution, _ durable.ToolExecutionApi) error {
		seen = execution.Command
		execution.Command += "\nGet-Date"
		return nil
	}})
	if _, err := run(tool, map[string]any{"command": "Get-Item"}, executionEnv, context.Background()); err != nil {
		t.Fatal(err)
	}
	if seen != "$p = 1\nGet-Item" {
		t.Fatalf("prepare saw %q, want the prefixed command", seen)
	}
	argv := executionEnv.commands[0]
	if script := argv[len(argv)-1]; !strings.HasSuffix(script, "$p = 1\nGet-Item\nGet-Date") {
		t.Fatalf("script = %q, want the prepared command", script)
	}
	failing := newProgramsEnv(t, []string{"pwsh"}, "never", 0)
	_, err := runFailing(t, CreatePowerShellTool(&PowerShellToolOptions{Prepare: func(context.Context, *BashExecution, durable.ToolExecutionApi) error {
		return errors.New("prepare refused")
	}}), map[string]any{"command": "1"}, failing)
	if err == nil || err.Error() != "prepare refused" || len(failing.commands) != 0 {
		t.Fatalf("error = %v, commands = %q, want the prepare error and no program run", err, failing.commands)
	}
}
