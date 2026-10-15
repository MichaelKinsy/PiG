package env

// Ports packages/env/test/conformance.test.ts

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/durabletest"
	durableenv "github.com/MichaelKinsy/PiG/durable/env"
	"github.com/MichaelKinsy/PiG/durable/env/node"
)

// conformanceShell is the shell the conformance cases use on Windows, where `sh` is not a program. Git Bash's `ln -s`
// copies instead of linking unless native symlinks are enabled.
func conformanceShell() (shell []string, noSymlinks bool) {
	if runtime.GOOS != "windows" {
		return nil, false
	}
	programFiles := os.Getenv("ProgramFiles")
	if programFiles == "" {
		programFiles = `C:\Program Files`
	}
	return []string{filepath.Join(programFiles, "Git", "bin", "bash.exe"), "-c"}, true
}

// Pi source: packages/env/src/connection.ts, packages/env/src/remote-env.ts
// mutation-checked: dropping the reads and writes of ConnectionOptions.Command, RemoteExecutionEnvOptions.Connection, RemoteExecutionEnvOptions.Cwd fails it
func TestRemoteExecutionEnvOverAPipeConformance(t *testing.T) {
	isolateGitConfiguration(t)
	connection := NewConnection(ConnectionOptions{Command: []string{daemonBinary(t)}})
	t.Cleanup(connection.Close)
	shell, noSymlinks := conformanceShell()
	// upstream: packages/env/test/conformance.test.ts:19
	durabletest.RegisterEnvConformance(t, "RemoteExecutionEnv over a pipe", func(use func(durableenv.ExecutionEnv) error) error {
		cwd := t.TempDir()
		return use(NewRemoteExecutionEnv(RemoteExecutionEnvOptions{
			Connection: connection, ID: "pi-env:test", Cwd: cwd, Watch: RemoteWatchOptions{PollIntervalMs: new(100)},
		}))
	}, durabletest.EnvConformanceRegisterOptions{Shell: shell, Symlinks: new(!noSymlinks), ListFiles: true})
}

// upstream: packages/env/test/conformance.test.ts:34 (LocalExecutionEnv); Pi source: packages/env/src/list-files.ts
// mutation-checked: dropping the reads and writes of LocalExecutionEnvOptions.Connection fails it
func TestLocalExecutionEnvConformance(t *testing.T) {
	isolateGitConfiguration(t)
	connection := NewConnection(ConnectionOptions{Command: []string{daemonBinary(t)}})
	t.Cleanup(connection.Close)
	shell, noSymlinks := conformanceShell()
	durabletest.RegisterEnvConformance(t, "LocalExecutionEnv", func(use func(durableenv.ExecutionEnv) error) error {
		return use(NewLocalExecutionEnv(LocalExecutionEnvOptions{
			NodeExecutionEnvOptions: node.NodeExecutionEnvOptions{Cwd: t.TempDir(), Watch: node.NodeWatchOptions{PollIntervalMs: new(100)}},
			Connection:              connection,
		}))
	}, durabletest.EnvConformanceRegisterOptions{Shell: shell, Symlinks: new(!noSymlinks), ListFiles: true})
}

// isolateGitConfiguration points the Git configuration the daemon reads for the global excludes file at nothing, so a listing
// does not depend on the machine's.
func isolateGitConfiguration(t *testing.T) {
	t.Helper()
	isolated := t.TempDir()
	t.Setenv("HOME", isolated)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(isolated, "config"))
	t.Setenv("GIT_CONFIG_GLOBAL", "")
	t.Setenv("GIT_CONFIG_SYSTEM", filepath.Join(isolated, "no-system-gitconfig"))
}
