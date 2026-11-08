package terminals

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

var (
	daemonOnce sync.Once
	daemonPath string
	daemonErr  error
)

// useDaemon makes the pi-env daemon this repository builds (`./env/cmd/pi-env`) the one terminals use on this machine
// (P2_TERMINAL_DAEMON), and a plain shell the one they run, without the developer's rc files. Pi's tests skip when there
// is no daemon build; the Go daemon is built from source here, so a failure to build one fails the test.
func useDaemon(t *testing.T) {
	t.Helper()
	daemonOnce.Do(func() {
		directory, err := os.MkdirTemp("", "p2-terminals-daemon-")
		if err != nil {
			daemonErr = err
			return
		}
		daemonPath = filepath.Join(directory, "pi-env")
		build := exec.Command("go", "build", "-trimpath", "-o", daemonPath, "github.com/MichaelKinsy/PiG/env/cmd/pi-env")
		build.Dir = testenv.ModuleRoot(t)
		build.Env = append(os.Environ(), "CGO_ENABLED=0")
		if output, err := build.CombinedOutput(); err != nil {
			daemonErr = &buildError{err: err, output: string(output)}
		}
	})
	if daemonErr != nil {
		t.Fatalf("building the pi-env daemon: %v", daemonErr)
	}
	t.Setenv("P2_TERMINAL_DAEMON", daemonPath)
	// The host and its shells inherit this: a plain shell, without the developer's rc files.
	t.Setenv("SHELL", plainShell())
}

// plainShell is the shell tests run: Pi's tests set `/bin/sh`, which is bash on the machines they are written on (macOS:
// readline echoes what was typed ahead, so a program's output starts its own line and the prompt matches `[$#] $`). Where
// `/bin/sh` is dash (Debian), the tty echoes typed-ahead input before the prompt and the output follows the prompt on its
// line, so bash is the shell there.
func plainShell() string {
	if path, err := exec.LookPath("bash"); err == nil {
		return path
	}
	return "/bin/sh"
}

type buildError struct {
	err    error
	output string
}

func (e *buildError) Error() string { return e.err.Error() + "\n" + e.output }

// poll waits until check holds (Pi's `poll`: 50 ms steps, 10 s by default).
func poll(t testing.TB, check func() bool, timeout ...time.Duration) {
	t.Helper()
	limit := 10 * time.Second
	if len(timeout) > 0 {
		limit = timeout[0]
	}
	deadline := time.Now().Add(limit)
	for !check() {
		if time.Now().After(deadline) {
			t.Fatal("Timed out")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func realpath(path string) (string, error) { return filepath.EvalSymlinks(path) }
