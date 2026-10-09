package env

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

func execOutput(t *testing.T, env *RemoteExecutionEnv, command any, options *durableenv.ShellExecOptions) string {
	t.Helper()
	var mu sync.Mutex
	var out strings.Builder
	if options == nil {
		options = &durableenv.ShellExecOptions{}
	}
	options.OnOutput = func(_ context.Context, text string, _ durableenv.ShellOutputInfo) {
		mu.Lock()
		defer mu.Unlock()
		out.WriteString(text)
	}
	must(env.Exec(background, command, options))
	mu.Lock()
	defer mu.Unlock()
	return out.String()
}

// packages/env/src/remote-env.ts RemoteExecutionEnvOptions.shellEnv: added to the remote environment of every command that inherits
// its environment; a per-command env value wins, and inheritEnv: false leaves it out.
// mutation-checked: dropping the reads and writes of RemoteExecutionEnvOptions.ShellEnv fails it
// Pi: packages/env/src/remote-env.ts:54 (shellEnv)
// its environment; a per-command env value wins, and inheritEnv: false leaves it out (remote-env.ts:860).
func TestRemoteExecutionEnvShellEnvReachesInheritingCommands(t *testing.T) {
	connection := NewConnection(ConnectionOptions{Command: []string{daemonBinary(t)}})
	t.Cleanup(connection.Close)
	env := NewRemoteExecutionEnv(RemoteExecutionEnvOptions{
		Connection: connection, ID: "pi-env:shell-env", Cwd: t.TempDir(),
		ShellEnv: map[string]string{"PIG_SHELL_ENV_PROBE": "from-shell-env", "PIG_SHELL_ENV_OTHER": "kept"},
	})
	probe := `printf '%s|%s' "$PIG_SHELL_ENV_PROBE" "$PIG_SHELL_ENV_OTHER"`
	if got := execOutput(t, env, probe, nil); got != "from-shell-env|kept" {
		t.Fatalf("inheriting command saw %q", got)
	}
	override := execOutput(t, env, probe, &durableenv.ShellExecOptions{Env: map[string]string{"PIG_SHELL_ENV_PROBE": "per-command"}})
	if override != "per-command|kept" {
		t.Fatalf("per-command env did not win: %q", override)
	}
	inherit := false
	if got := execOutput(t, env, probe, &durableenv.ShellExecOptions{InheritEnv: &inherit}); got != "|" {
		t.Fatalf("inheritEnv=false still received shellEnv: %q", got)
	}
}

// RemoteExecutionEnvOptions.shellPath: an explicit shell on the remote machine runs string commands.
// mutation-checked: dropping the reads and writes of RemoteExecutionEnvOptions.ShellPath fails it
// Pi: packages/env/src/remote-env.ts:53 (shellPath)
// RemoteExecutionEnvOptions.shellPath: an explicit shell on the remote machine runs string commands (packages/env/src/remote-env.ts:878).
func TestRemoteExecutionEnvShellPathRunsStringCommands(t *testing.T) {
	shell := filepath.Join(t.TempDir(), "marker-shell")
	if err := os.WriteFile(shell, []byte("#!/bin/sh\nprintf 'custom-shell:'\nexec /bin/sh \"$@\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	connection := NewConnection(ConnectionOptions{Command: []string{daemonBinary(t)}})
	t.Cleanup(connection.Close)
	env := NewRemoteExecutionEnv(RemoteExecutionEnvOptions{Connection: connection, ID: "pi-env:shell-path", Cwd: t.TempDir(), ShellPath: shell})
	if got := execOutput(t, env, "printf done", nil); got != "custom-shell:done" {
		t.Fatalf("string command output = %q, want it to run through the configured shell", got)
	}
}

// packages/durable/src/env/index.ts Shell.cleanup: kills every command the environment still runs, for its owner's shutdown. The
// commands settle with their killed status instead of running to completion. Driven through the Shell interface.
// packages/durable/src/env/index.ts:321 Shell.cleanup.
func TestShellCleanupKillsRunningCommands(t *testing.T) {
	connection := NewConnection(ConnectionOptions{Command: []string{daemonBinary(t)}})
	t.Cleanup(connection.Close)
	var shell durableenv.Shell = NewRemoteExecutionEnv(RemoteExecutionEnvOptions{Connection: connection, ID: "pi-env:cleanup", Cwd: t.TempDir()})
	started := make(chan struct{})
	var once sync.Once
	type outcome struct {
		result durableenv.ShellExecResult
		err    error
	}
	settled := make(chan outcome, 1)
	go func() {
		result, err := shell.Exec(background, []string{"sh", "-c", "echo started; sleep 60"}, &durableenv.ShellExecOptions{
			OnOutput: func(context.Context, string, durableenv.ShellOutputInfo) { once.Do(func() { close(started) }) },
		})
		settled <- outcome{result, err}
	}()
	<-started
	if err := shell.Cleanup(background); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-settled:
		if got.err == nil && got.result.ExitCode == 0 {
			t.Fatalf("a killed command settled as a clean exit: %+v", got)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("Cleanup left the running command alive")
	}
}
