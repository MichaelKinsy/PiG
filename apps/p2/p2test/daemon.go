package p2test

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

var (
	daemonOnce sync.Once
	daemonPath string
	daemonErr  error
)

// UseTerminalDaemon makes the pi-env daemon this repository builds (`./env/cmd/pi-env`) the one terminals use on this
// machine (P2_TERMINAL_DAEMON), and a plain shell the one they run, without the developer's rc files. Pi's tests skip when
// there is no daemon build; the Go daemon is built from source here, so a failure to build one fails the test.
//
// The shell is bash where there is one: Pi's tests set `/bin/sh`, which is bash on the machines they are written on (macOS:
// readline echoes what was typed ahead, so a program's output starts its own line and the prompt matches `[$#] $`). Where
// `/bin/sh` is dash (Debian), the tty echoes typed-ahead input before the prompt and the output follows the prompt on its
// line.
func UseTerminalDaemon(t testing.TB) {
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
			daemonErr = &daemonBuildError{err: err, output: string(output)}
		}
	})
	if daemonErr != nil {
		t.Fatalf("building the pi-env daemon: %v", daemonErr)
	}
	t.Setenv("P2_TERMINAL_DAEMON", daemonPath)
	t.Setenv("SHELL", "/bin/sh")
}

type daemonBuildError struct {
	err    error
	output string
}

func (e *daemonBuildError) Error() string { return e.err.Error() + "\n" + e.output }
