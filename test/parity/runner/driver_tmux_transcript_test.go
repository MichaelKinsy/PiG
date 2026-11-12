//go:build parity

package runner

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The transcript reader must not depend on the host's cat. On Ubuntu 26.04 /usr/bin/cat is uutils coreutils 0.8.0: reading tmux pipe-pane's socket it copies the first chunk and then waits in the kernel for more, so every later byte of the stream (the last frames of a run) never reached the file and the final_frame comparators read a truncated transcript. This test puts a cat with that behavior first in the PATH of a private tmux server and requires every chunk the pane writes to reach the file.
func TestTranscriptReaderKeepsEveryChunkWhenCatStalls(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the tmux driver is not used on Windows")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	bin := t.TempDir()
	stall := "#!/bin/sh\nhead -c1 >/dev/null\nexec sleep 30\n"
	if err := os.WriteFile(filepath.Join(bin, "cat"), []byte(stall), 0o755); err != nil { //nolint:gosec // a fixture script must be executable.
		t.Fatal(err)
	}
	socket := fmt.Sprintf("pig-parity-transcript-%d", os.Getpid())
	tmux := func(args ...string) *exec.Cmd {
		//nolint:gosec // G204 (class a): program is literal tmux; args are fixed options and test-local names.
		cmd := exec.Command("tmux", append([]string{"-L", socket, "-f", os.DevNull}, args...)...)
		cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		return cmd
	}
	if out, err := tmux("new-session", "-d", "-s", "t", "-x", "80", "-y", "24", "sleep 0.5; printf one-; sleep 0.3; printf two-; sleep 0.3; printf three-; sleep 30").CombinedOutput(); err != nil {
		t.Fatalf("tmux new-session: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = tmux("kill-session", "-t", "t").Run() })
	path := filepath.Join(t.TempDir(), "terminal-transcript.log")
	if out, err := tmux("pipe-pane", "-o", "-t", "t", transcriptReaderCommand(path)).CombinedOutput(); err != nil {
		t.Fatalf("tmux pipe-pane: %v: %s", err, out)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		raw, _ := os.ReadFile(path)
		if strings.Contains(string(raw), "one-two-three-") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("transcript = %q after 5s, want it to hold every chunk the pane wrote (one-two-three-)", raw)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
