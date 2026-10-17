package env

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// upstream: packages/env/src/connection.ts:307-320 #teardown ends the daemon's stdin and calls child.kill(), Node's
// SIGTERM, so a daemon reached through ssh can end its remote side before it exits.
// mutation-checked: the mutant "teardown kills the child with SIGKILL" fails it.
func TestConnectionTeardownEndsTheDaemonWithSIGTERM(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the wrapper is a POSIX shell script")
	}
	directory := t.TempDir()
	marker := filepath.Join(directory, "terminated")
	wrapper := filepath.Join(directory, "daemon")
	// The wrapper runs the daemon on its own stdin (fd 3: a background job reads /dev/null) and records the signal that ends the wrapper.
	script := "#!/bin/sh\ntrap 'echo TERM > " + marker + "; kill $child; exit 143' TERM\nexec 3<&0\n\"" + daemonBinary(t) + "\" \"$@\" <&3 3<&- &\nchild=$!\nwait $child\n"
	writeProgram(t, wrapper, script)
	connection := NewConnection(ConnectionOptions{Command: []string{wrapper}})
	t.Cleanup(connection.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	must(connection.Info(ctx))
	connection.Close()
	deadline := time.Now().Add(10 * time.Second)
	for {
		content, readErr := os.ReadFile(marker)
		if readErr == nil && strings.TrimSpace(string(content)) == "TERM" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the daemon process did not receive SIGTERM (marker %q, %v)", content, readErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
