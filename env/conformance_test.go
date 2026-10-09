package env

// Ports packages/env/test/conformance.test.ts

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/durabletest"
	durableenv "github.com/MichaelKinsy/PiG/durable/env"
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
	connection := NewConnection(ConnectionOptions{Command: []string{daemonBinary(t)}})
	t.Cleanup(connection.Close)
	shell, noSymlinks := conformanceShell()
	// upstream: packages/env/test/conformance.test.ts:19
	durabletest.RegisterEnvConformance(t, "RemoteExecutionEnv over a pipe", func(use func(durableenv.ExecutionEnv) error) error {
		cwd := t.TempDir()
		return use(NewRemoteExecutionEnv(RemoteExecutionEnvOptions{
			Connection: connection, ID: "pi-env:test", Cwd: cwd, Watch: RemoteWatchOptions{PollIntervalMs: new(100)},
		}))
	}, durabletest.EnvConformanceRegisterOptions{Shell: shell, Symlinks: new(!noSymlinks)})
}
