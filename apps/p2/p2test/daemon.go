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
// machine (P2_TERMINAL_DAEMON), and a plain `sh` the one they run, without the developer's rc files. Pi's tests skip when
// there is no daemon build; the Go daemon is built from source here, so a failure to build one fails the test.
//
// Pi's tests set SHELL=/bin/sh, which is bash in POSIX mode on the machines they are written on (macOS): readline echoes
// what was typed ahead after the prompt, so a program's output starts its own line and the prompt matches `[$#] $`. Where
// /bin/sh is dash (Debian, Ubuntu), the tty echoes typed-ahead input before the prompt and the output follows the prompt
// on its line. There the shell is the same thing Pi's machines have: bash --posix, started without rc files, prompting
// `$ `.
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
	t.Setenv("SHELL", plainShell(t))
	t.Setenv("PS1", "$ ")
}

// plainShell is a `sh` script that is bash in POSIX mode without rc files and without bracketed paste: readline asks the
// terminal for mode 2004 at every prompt (bash 5.1 and later), which Pi's /bin/sh (dash) never does, and the terminal tests
// of paste check that a program that did not ask for it gets plain text.
func plainShell(t testing.TB) string {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		return "/bin/sh"
	}
	directory := t.TempDir()
	inputrc := filepath.Join(directory, "inputrc")
	if err := os.WriteFile(inputrc, []byte("set enable-bracketed-paste off\n"), 0o644); err != nil {
		t.Fatalf("writing the inputrc: %v", err)
	}
	path := filepath.Join(directory, "sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nINPUTRC="+inputrc+" exec "+bash+" --posix --norc --noprofile \"$@\"\n"), 0o755); err != nil {
		t.Fatalf("writing the sh wrapper: %v", err)
	}
	return path
}

type daemonBuildError struct {
	err    error
	output string
}

func (e *daemonBuildError) Error() string { return e.err.Error() + "\n" + e.output }
