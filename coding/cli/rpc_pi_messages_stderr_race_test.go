//go:build unix

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runPiMessagesRPC reports the child's stderr in the error it returns when the child ends its stdout before the first assistant turn completes. os/exec
// copies the child's stderr into the buffer on a goroutine of its own until the child exits, and Cmd.Wait joins it: reading the buffer before the child
// is waited for races with the copy, and -race fails this test while runPiMessagesRPC does that.
func TestRunPiMessagesRPCReportsTheChildStderrAfterTheChildEnded(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "child.sh")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nread -r _\necho late >&2\nexec 1>&-\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := runPiMessagesRPC(t.Context(), bin, t.TempDir(), "http://127.0.0.1:1", make(chan struct{}, 1))
	if err == nil || !strings.Contains(err.Error(), "no completed first assistant turn\nstderr: late\n") {
		t.Fatalf("runPiMessagesRPC error = %v, want the child's stderr after \"no completed first assistant turn\"", err)
	}
}
