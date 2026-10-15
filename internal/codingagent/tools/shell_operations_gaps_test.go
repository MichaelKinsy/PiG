package tools

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
)

type recordingShellOperations struct {
	command, cwd string
	env          []string
}

func (o *recordingShellOperations) Exec(_ context.Context, command, cwd string, opts BashOperationsExecOptions) (BashOperationsResult, error) {
	o.command, o.cwd, o.env = command, cwd, opts.Env
	opts.OnData([]byte("done\n"))
	return BashOperationsResult{ExitCode: new(0)}, nil
}

func execCollect(t *testing.T, ops *LocalShellOperations, command, cwd string, opts BashOperationsExecOptions) (string, BashOperationsResult, error) {
	t.Helper()
	var out strings.Builder
	opts.OnData = func(data []byte) { out.Write(data) }
	result, err := ops.Exec(t.Context(), command, cwd, opts)
	return out.String(), result, err
}

// bash.ts createLocalBashOperations(options): commands run through the resolved shell in the given directory, the bin directory leads PATH when no environment is passed, an explicit environment replaces it, an explicit shell path is used, and a shell that does not exist fails the command.
// Pi: packages/coding-agent/src/core/tools/bash.ts createLocalBashOperations (:174).
func TestCreateLocalBashOperationsRunsCommandsThroughTheResolvedShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX shell syntax")
	}
	cwd, bin := t.TempDir(), t.TempDir()
	ops := CreateLocalBashOperations(nil)
	if ops.ShellName != "bash" {
		t.Fatalf("shell name = %q", ops.ShellName)
	}
	out, result, err := execCollect(t, ops, "printf %s \"$(pwd -P)\"", cwd, BashOperationsExecOptions{})
	want, _ := filepath.EvalSymlinks(cwd)
	if err != nil || result.ExitCode == nil || *result.ExitCode != 0 || out != want {
		t.Fatalf("pwd = %q, %+v, %v; want %q exit 0", out, result, err, want)
	}
	if _, result, err := execCollect(t, ops, "exit 3", cwd, BashOperationsExecOptions{}); err != nil || result.ExitCode == nil || *result.ExitCode != 3 {
		t.Fatalf("exit 3 = %+v, %v", result, err)
	}

	withBin := CreateLocalBashOperations(&LocalBashOptions{BinDir: bin})
	out, _, err = execCollect(t, withBin, "printf %s \"$PATH\"", cwd, BashOperationsExecOptions{})
	if err != nil || !strings.HasPrefix(out, bin+string(os.PathListSeparator)) {
		t.Fatalf("PATH = %q (%v), want it to start with %q", out, err, bin)
	}
	out, _, err = execCollect(t, withBin, "printf %s \"$PI_TEST_ENV:$PATH\"", cwd, BashOperationsExecOptions{Env: []string{"PI_TEST_ENV=set", "PATH=/usr/bin:/bin"}})
	if err != nil || out != "set:/usr/bin:/bin" {
		t.Fatalf("explicit env = %q (%v), want it to replace the inherited one", out, err)
	}

	sh, lookErr := exec.LookPath("sh")
	if lookErr != nil {
		t.Skip("no sh")
	}
	explicit := CreateLocalBashOperations(&LocalBashOptions{ShellPath: sh})
	if out, _, err := execCollect(t, explicit, "printf ok", cwd, BashOperationsExecOptions{}); err != nil || out != "ok" {
		t.Fatalf("explicit shell path = %q, %v", out, err)
	}
	missing := CreateLocalBashOperations(&LocalBashOptions{ShellPath: filepath.Join(cwd, "no-such-shell")})
	if _, _, err := execCollect(t, missing, "printf ok", cwd, BashOperationsExecOptions{}); err == nil {
		t.Fatal("a missing shell path did not fail the command")
	}
	if _, _, err := execCollect(t, ops, "printf ok", filepath.Join(cwd, "missing-dir"), BashOperationsExecOptions{}); err == nil || !strings.Contains(err.Error(), "Cannot execute bash commands") {
		t.Fatalf("missing working directory = %v", err)
	}
}

// createLocalPowerShellOperations resolves PowerShell through getPowerShellConfig, so a platform without it fails the command with that error before anything runs.
// Pi: packages/coding-agent/src/core/tools/powershell.ts createLocalPowerShellOperations, getPowerShellConfig.
func TestLocalPowerShellOperationsResolvePowerShellWhenRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PowerShell resolves on Windows")
	}
	ops := NewLocalPowerShellOperations()
	_, _, err := execCollect(t, ops, "Get-Date", t.TempDir(), BashOperationsExecOptions{})
	if err == nil || err.Error() != "The powershell tool is only available on Windows." {
		t.Fatalf("Exec = %v, want the Windows-only error", err)
	}
	cfg, cfgErr := GetPowerShellConfig()
	if cfgErr == nil || cfgErr.Error() != err.Error() || cfg.Path != "" || len(cfg.Args) != 0 {
		t.Fatalf("GetPowerShellConfig = %+v, %v", cfg, cfgErr)
	}
}

// bash.ts spawnHook / powershell.ts PowerShellSpawnHook: the hook receives the command, working directory and environment the tool is about to run, and what it returns is what runs; a PowerShell hook is the same function type.
// Pi: packages/coding-agent/src/core/tools/bash.ts BashSpawnHook (:68), powershell.ts PowerShellSpawnHook (:25).
func TestSpawnHooksRewriteWhatRuns(t *testing.T) {
	cwd, other := t.TempDir(), t.TempDir()
	var seen BashSpawnContext
	var hook BashSpawnHook = func(spawn BashSpawnContext) BashSpawnContext {
		seen = spawn
		return BashSpawnContext{Command: "rewritten " + spawn.Command, CWD: other, Env: append(append([]string(nil), spawn.Env...), "HOOK=1")}
	}

	ops := &recordingShellOperations{}
	params, _ := json.Marshal(map[string]any{"command": "original"})
	for name, tool := range map[string]agent.AgentTool{
		"bash":       &BashTool{CWD: cwd, Operations: ops, SpawnHook: hook},
		"powershell": &PowerShellTool{CWD: cwd, Operations: ops, SpawnHook: hook},
	} {
		*ops = recordingShellOperations{}
		seen = BashSpawnContext{}
		if _, err := tool.Execute(t.Context(), "call", params, nil); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if seen.Command != "original" || seen.CWD != cwd {
			t.Errorf("%s: the hook saw %+v, want the tool's own command and directory", name, seen)
		}
		if ops.command != "rewritten original" || ops.cwd != other {
			t.Errorf("%s: ran %q in %q, want the hook's command and directory", name, ops.command, ops.cwd)
		}
		if v, ok := lookup(ops.env, "HOOK"); !ok || v != "1" {
			t.Errorf("%s: the hook's environment did not reach the operations: %v", name, ops.env)
		}
	}
}

// bash.ts createShellToolDefinition: a configured command prefix is prepended with a newline before the spawn hook sees the command.
// Pi: packages/coding-agent/src/core/tools/bash.ts (commandPrefix, resolveSpawnContext).
func TestBashToolCommandPrefixReachesTheSpawnHookAndOperations(t *testing.T) {
	ops := &recordingShellOperations{}
	var seen string
	tool := &BashTool{CWD: t.TempDir(), Operations: ops, CommandPrefix: "shopt -s expand_aliases", SpawnHook: func(spawn BashSpawnContext) BashSpawnContext {
		seen = spawn.Command
		return spawn
	}}
	params, _ := json.Marshal(map[string]any{"command": "ls"})
	if _, err := tool.Execute(t.Context(), "call", params, nil); err != nil {
		t.Fatal(err)
	}
	if want := "shopt -s expand_aliases\nls"; seen != want || ops.command != want {
		t.Fatalf("hook saw %q, operations ran %q, want %q", seen, ops.command, want)
	}
}

// createLocalShellOperations(shellName, resolveShellConfig) with a command wrapper: the wrapper rewrites the command before the shell runs it.
// Pi: packages/coding-agent/src/core/tools/powershell.ts createLocalPowerShellOperations (command prefix).
func TestLocalShellOperationsWrapCommandBeforeTheShellRuns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell")
	}
	ops := &LocalShellOperations{
		ShellName:    "sh",
		ResolveShell: func() (ShellConfig, error) { return ShellConfig{Path: "/bin/sh", Args: []string{"-c"}}, nil },
		WrapCommand:  func(command string) string { return "printf wrapped-;" + command },
	}
	out, _, err := execCollect(t, ops, "printf end", t.TempDir(), BashOperationsExecOptions{})
	if err != nil || out != "wrapped-end" {
		t.Fatalf("output = %q, %v; want the wrapper's prefix before the command", out, err)
	}
}

// find.ts FindOperations: exists is called with the resolved search path and must exist, glob is optional (without it the local fd search runs), and an exists operation is required once a glob operation is configured.
// Pi: packages/coding-agent/src/core/tools/find.ts FindOperations (:53-55), execute.
func TestFindOperationsExistsGateGlobAndFallBackToFdWithoutGlob(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var existsArg string
	globCalled := false
	glob := func(string, string, FindGlobOptions) ([]string, error) {
		globCalled = true
		return []string{filepath.Join(dir, "a.txt")}, nil
	}
	tool := &FindTool{CWD: dir, Operations: &FindOperations{Exists: func(path string) (bool, error) { existsArg = path; return false, nil }, Glob: glob}}
	result := runFileTool(t, tool, t.Context(), map[string]any{"pattern": "*.txt"})
	if want := mustResolvePath(t, dir, "."); existsArg != want || !result.IsError || result.Text() != "Path not found: "+want || globCalled {
		t.Fatalf("exists arg %q, result %q error %v, glob called %v", existsArg, result.Text(), result.IsError, globCalled)
	}

	noExists := &FindTool{CWD: dir, Operations: &FindOperations{Glob: glob}}
	result = runFileTool(t, noExists, t.Context(), map[string]any{"pattern": "*.txt"})
	if !result.IsError || result.Text() != "ops.exists is not a function" || globCalled {
		t.Fatalf("a glob operation without exists = %q error %v glob called %v", result.Text(), result.IsError, globCalled)
	}

	// Without a glob operation the exists callback is not consulted and fd searches the directory.
	existsOnly := &FindTool{CWD: dir, FdPath: requireFD(t), Operations: &FindOperations{Exists: func(string) (bool, error) { return false, nil }}}
	result = runFileTool(t, existsOnly, t.Context(), map[string]any{"pattern": "*.txt"})
	if result.IsError || result.Text() != "a.txt" {
		t.Fatalf("fd fallback = %q error %v", result.Text(), result.IsError)
	}
}

// grep.ts GrepOperations.isDirectory: the local default reports directories and files and fails for a missing path.
// Pi: packages/coding-agent/src/core/tools/grep.ts GrepOperations (:60), defaultGrepOperations.
func TestGrepDefaultOperationsReportDirectoriesFilesAndMissingPaths(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ops := (&GrepTool{}).operations()
	if isDir, err := ops.IsDirectory(dir); err != nil || !isDir {
		t.Fatalf("directory = %v, %v", isDir, err)
	}
	if isDir, err := ops.IsDirectory(file); err != nil || isDir {
		t.Fatalf("file = %v, %v", isDir, err)
	}
	if _, err := ops.IsDirectory(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("a missing path did not fail")
	}
	custom := &GrepTool{Operations: &GrepOperations{IsDirectory: func(string) (bool, error) { return true, nil }}}
	if isDir, err := custom.operations().IsDirectory(file); err != nil || !isDir {
		t.Fatalf("custom operations = %v, %v", isDir, err)
	}
}

// shell.ts getShellConfig(customShellPath): an existing custom path is the shell, a missing one is an error naming it, and an empty path selects the platform default.
// Pi: packages/coding-agent/src/utils/shell.ts getShellConfig.
func TestGetShellConfigResolvesCustomMissingAndDefaultPaths(t *testing.T) {
	dir := t.TempDir()
	custom := filepath.Join(dir, "my-shell")
	if err := os.WriteFile(custom, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg, err := GetShellConfig(custom)
	if err != nil || cfg.Path != custom {
		t.Fatalf("custom shell = %+v, %v; want it to be the shell", cfg, err)
	}
	missing := filepath.Join(dir, "absent")
	if got, err := GetShellConfig(missing); err == nil || err.Error() != "Custom shell path not found: "+missing || !reflect.DeepEqual(got, ShellConfig{}) {
		t.Fatalf("missing custom shell error = %v", err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	def, err := GetShellConfig("")
	if err != nil || def.Path == "" || def.Path == custom {
		t.Fatalf("default shell = %+v, %v", def, err)
	}
	want, wantErr := defaultShellConfig()
	if wantErr != nil || !reflect.DeepEqual(def, want) {
		t.Fatalf("GetShellConfig(\"\") = %+v, want the platform default %+v", def, want)
	}
}
