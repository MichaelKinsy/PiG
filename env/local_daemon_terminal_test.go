//go:build !windows

package env

// Pi source: packages/env/src/ssh.ts:362 (localDaemon) and packages/env/README.md ("`localDaemon()` is the daemon for this
// machine, for terminals without SSH (`new Connection({ command: [localDaemon()] })`)").

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// The released daemon, built as `make build-env-daemons` lays it out, is what LocalDaemon names, and a Connection
// started with it opens terminals.
func TestOpenTerminalOverTheLocalDaemonOfThePackagedLayout(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("PI_ENV_DAEMON_DIR", directory)
	packaged, err := LocalDaemon()
	if err != nil {
		t.Fatalf("no packaged daemon layout for this system: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(packaged), 0o755); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-trimpath", "-o", packaged, "./env/cmd/pi-env")
	build.Dir = testenv.ModuleRoot(t)
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the daemon: %v\n%s", err, output)
	}

	connection := NewConnection(ConnectionOptions{Command: []string{packaged}})
	t.Cleanup(connection.Close)
	output := newCollector()
	terminal, err := OpenTerminal(background, connection, TerminalOptions{
		Cwd: t.TempDir(), Argv: []string{"sh", "-c", "test -t 0 && echo LOCAL-TTY; exit 4"}, Cols: 80, Rows: 24, OnData: output.onData,
	})
	if err != nil {
		t.Fatal(err)
	}
	if code, err := waitExit(t, terminal); err != nil || code != 4 {
		t.Fatalf("exit = %d, %v", code, err)
	}
	output.waitFor(t, "LOCAL-TTY")
}
