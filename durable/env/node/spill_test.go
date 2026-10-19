package node

// Ported from packages/durable/test/env-node-spill.test.ts at v1.0.0.

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// Upstream mocks fs.createWriteStream so the spill stream is slow and
// immediately backpressured. Spill writes here are synchronous in the output
// pump, which is how a pending spill write stops the pump reading: the first
// write is held pending instead.
//
// Nothing here is ordered by a delay, which a loaded machine overruns (starting a process can take
// seconds there). The shell exits only after the spill write is pending, so the exit grace starts with the
// write in flight. The descendant writes only after that grace has expired (testHookGraceExpired runs at
// the expiry), and the write is released only after the descendant has written. Without the pending-spill
// guard, settlement closes stdout at the expiry and the descendant's output is lost.
func TestSpillBackpressureKeepsInheritedStdioOpenPastTheExitGracePeriodWhileASpillWriteIsPending(t *testing.T) {
	skipOnWindows(t)
	env, root := newTestEnv(t)
	writePending := filepath.Join(root, "write-pending")
	graceExpired := filepath.Join(root, "grace-expired")
	descendantWrote := filepath.Join(root, "descendant-wrote")
	releaseWrite := make(chan struct{})
	var spillWrites atomic.Int32
	original := writeSpillChunk
	writeSpillChunk = func(file *os.File, chunk []byte) (int, error) {
		if spillWrites.Add(1) == 1 {
			if err := os.WriteFile(writePending, nil, 0o600); err != nil {
				t.Error(err)
			}
			<-releaseWrite
		}
		return original(file, chunk)
	}
	var expired sync.Once
	testHookGraceExpired = func() {
		expired.Do(func() {
			if err := os.WriteFile(graceExpired, nil, 0o600); err != nil {
				t.Error(err)
			}
		})
	}
	t.Cleanup(func() { writeSpillChunk, testHookGraceExpired = original, nil })
	waitFor := func(path string) string {
		return "while [ ! -e " + toBashSingleQuotedArg(path) + " ]; do sleep 0.01; done"
	}
	command := "(" + waitFor(graceExpired) + "; printf '%01000d' 0 | tr 0 b; : > " + toBashSingleQuotedArg(descendantWrote) + ") &\n" +
		"printf '%020d' 0 | tr 0 a\n" +
		waitFor(writePending)

	done := make(chan struct{})
	var output string
	var result durableenv.ShellExecResult
	var execErr error
	go func() {
		defer close(done)
		result, output, execErr = collectShellOutput(env, command, &durableenv.ShellExecOptions{Spill: &durableenv.ShellSpillOptions{AfterBytes: 10, AfterLines: 10}}, background)
	}()
	go func() {
		for {
			if _, err := os.Stat(descendantWrote); err == nil {
				close(releaseWrite)
				return
			}
			select {
			case <-done:
				return
			case <-time.After(5 * time.Millisecond):
			}
		}
	}()
	<-done
	mustDo(t, execErr)
	if spillWrites.Load() == 0 {
		t.Fatal("no spill write happened")
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
