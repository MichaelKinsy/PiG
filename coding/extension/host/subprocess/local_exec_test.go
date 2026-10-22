package subprocess

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
)

// LocalExec is the command of createLocalBashOperations().exec for an extension in another process (the connection handler) and for a fused one (a direct
// call). Pi's exec rejects with "timeout:<seconds>" when the timeout elapses, and a failure to deliver output ends the command with that failure.
func TestLocalExecReportsPisTimeoutAndEndsWithADeliveryFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the commands are POSIX shell commands, which on Windows need a Git bash or WSL the Windows runners do not guarantee")
	}
	one := 1.0
	var got strings.Builder
	_, err := LocalExec(context.Background(), BuiltinToolLocalExecArgs{Shell: "bash", Command: "echo begun; exec sleep 30", Cwd: t.TempDir(), Timeout: &one}, "", func(data []byte) error {
		got.Write(data)
		return nil
	})
	if err == nil || err.Error() != "timeout:1" {
		t.Fatalf("a command past its timeout ended with %v, want Pi's timeout:1", err)
	}
	if got.String() != "begun\n" {
		t.Fatalf("output before the timeout = %q, want %q", got.String(), "begun\n")
	}

	refused := errors.New("the reader is gone")
	_, err = LocalExec(context.Background(), BuiltinToolLocalExecArgs{Shell: "bash", Command: "echo begun; exec sleep 30", Cwd: t.TempDir()}, "", func([]byte) error { return refused })
	if !errors.Is(err, refused) {
		t.Fatalf("a command whose output could not be delivered ended with %v, want the delivery failure", err)
	}

	aborted, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = LocalExec(aborted, BuiltinToolLocalExecArgs{Shell: "bash", Command: "echo never", Cwd: t.TempDir()}, "", func([]byte) error { return nil })
	if err == nil || err.Error() != "aborted" {
		t.Fatalf("a command whose context ended before it began ended with %v, want Pi's aborted", err)
	}
}
