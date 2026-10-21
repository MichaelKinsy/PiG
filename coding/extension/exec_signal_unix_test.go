//go:build unix

package extension_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// execSignalScript writes its output, records its pid, and becomes `sleep 60`.
const execSignalScript = `echo started; echo $$ > "$1.tmp"; mv "$1.tmp" "$1"; exec sleep 60`

func waitForPid(t *testing.T, pidFile string) int {
	t.Helper()
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if data, err := os.ReadFile(pidFile); err == nil {
			pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err != nil {
				t.Fatal(err)
			}
			return pid
		}
	}
	t.Fatal("the command never recorded its pid")
	return 0
}

// core/exec.ts:46-52: options.signal aborting kills the command with SIGTERM, and the call resolves with the output so far, killed set and code 0,
// the code `code ?? 0` gives a command that a signal ended. The command is gone when the call returns.
func TestExecCommandOptionsSignalEndsTheCommand(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	signal, abort := context.WithCancel(context.Background())
	defer abort()
	go func() {
		for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if _, err := os.Stat(pidFile); err == nil {
				break
			}
		}
		abort()
	}()
	result, err := extension.ExecCommand(context.Background(), t.TempDir(), "sh", []string{"-c", execSignalScript, "sh", pidFile}, &extension.ExecOptions{Signal: signal})
	if err != nil {
		t.Fatal(err)
	}
	if result.Stdout != "started\n" || result.Code != 0 || !result.Killed {
		t.Fatalf("result = %+v, want the output so far, code 0 and killed", result)
	}
	if err := syscall.Kill(waitForPid(t, pidFile), 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("the command is still running after the call returned (kill 0: %v)", err)
	}
}

// exec.ts:42-48 starts the command and kills it at once when the signal is aborted already.
func TestExecCommandOptionsSignalAbortedBeforeTheCallKillsTheStartedCommand(t *testing.T) {
	signal, abort := context.WithCancel(context.Background())
	abort()
	result, err := extension.ExecCommand(context.Background(), t.TempDir(), "sleep", []string{"60"}, &extension.ExecOptions{Signal: signal})
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != 0 || !result.Killed {
		t.Fatalf("result = %+v, want code 0 and killed", result)
	}
}
