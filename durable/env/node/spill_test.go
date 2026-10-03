package node

// Ported from packages/durable/test/env-node-spill.test.ts at v1.0.0.

import (
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// Longer than the shell's post-exit stdio grace period plus the descendant's
// delayed write.
const spillWriteDelay = 600 * time.Millisecond

// Upstream mocks fs.createWriteStream so the spill stream is slow and
// immediately backpressured. Spill writes here are synchronous in the output
// pump, which is how a pending spill write stops the pump reading: every write
// is slowed instead.
func TestSpillBackpressureKeepsInheritedStdioOpenPastTheExitGracePeriodWhileASpillWriteIsPending(t *testing.T) {
	skipOnWindows(t)
	original := writeSpillChunk
	t.Cleanup(func() { writeSpillChunk = original })
	var slowWrites atomic.Int32
	writeSpillChunk = func(file *os.File, chunk []byte) (int, error) {
		slowWrites.Add(1)
		time.Sleep(spillWriteDelay)
		return original(file, chunk)
	}
	env, _ := newTestEnv(t)
	// The shell exits after the first chunk crosses the spill threshold and
	// blocks the spill. A background descendant retains stdout and writes after
	// the post-exit grace period. Without the pending-spill guard, settlement
	// closes stdout before that descendant output is read.
	command := "printf '%020d' 0 | tr 0 a; (sleep 0.2; printf '%01000d' 0 | tr 0 b) &"
	done := make(chan struct{})
	var output string
	var result durableenv.ShellExecResult
	var execErr error
	go func() {
		defer close(done)
		result, output, execErr = collectShellOutput(env, command, &durableenv.ShellExecOptions{Spill: &durableenv.ShellSpillOptions{AfterBytes: 10, AfterLines: 10}}, background)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("exec did not settle within 10s")
	}
	mustDo(t, execErr)
	if slowWrites.Load() == 0 {
		t.Fatal("no spill write was slowed")
	}
	if len(output) != 1020 {
		t.Fatalf("output has %d characters, want 1020", len(output))
	}
	if result.SpillPath == "" {
		t.Fatal("no spill path")
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(result.SpillPath)) })
	want := strings.Repeat("a", 20) + strings.Repeat("b", 1000)
	if got := must(env.ReadTextFile(background, result.SpillPath)); got != want {
		t.Fatalf("spill = %q", got)
	}
}
