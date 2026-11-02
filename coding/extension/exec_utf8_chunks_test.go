package extension_test

// pi: packages/coding-agent/test/exec-utf8-chunks.test.ts (#11360)

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/childwait"
)

func init() {
	// split-incomplete: a lone first byte of a three-byte character on each stream, then a normal exit.
	treeHelpers["split-incomplete"] = func([]string) {
		_, _ = os.Stdout.Write([]byte{0xe2})
		_, _ = os.Stderr.Write([]byte{0xf0})
	}
	// stepwrite DIR STREAM: the writes of exec-utf8-chunks.test.ts, each released by the test through a file in DIR once the call's readers have the previous
	// one: STREAM gets 61 e2, the other stream e2, then STREAM 82 ac. The child then waits to be ended.
	treeHelpers["stepwrite"] = func(args []string) {
		dir := args[0]
		stream, other := os.Stdout, os.Stderr
		if args[1] == "stderr" {
			stream, other = os.Stderr, os.Stdout
		}
		await := func(name string) {
			for deadline := time.Now().Add(holderLifetime); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
				if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
					return
				}
			}
		}
		_, _ = stream.Write([]byte{0x61, 0xe2})
		await("go2")
		_, _ = other.Write([]byte{0xe2})
		await("go3")
		_, _ = stream.Write([]byte{0x82, 0xac})
		time.Sleep(holderLifetime)
	}
}

// exec-utf8-chunks.test.ts: when either stream exceeds the shared cap, the UTF-8 bytes a decoder holds pending on either stream are discarded, never flushed as a
// replacement character. The test lets the child write one chunk at a time and waits until the call's readers hold it.
func TestExecCommandDiscardsPendingUTF8WhenAStreamExceedsTheCap(t *testing.T) {
	for _, overflowStream := range []string{"stdout", "stderr"} {
		t.Run(overflowStream, func(t *testing.T) {
			dir := t.TempDir()
			chunks := make(chan struct{}, 8)
			ctx := extension.WithExecDrainHooks(context.Background(), &childwait.Hooks{
				AfterData: func(int, []byte) { chunks <- struct{}{} },
			})
			command, args := helper(t, "stepwrite", dir, overflowStream)
			done := make(chan execOutcome, 1)
			go func() {
				result, err := extension.ExecCommand(ctx, t.TempDir(), command, args, maxBuffer(3))
				done <- execOutcome{result, err}
			}()
			release := func(name string) {
				<-chunks
				if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
					t.Error(err)
				}
			}
			release("go2")
			release("go3")
			outcome := <-done
			if outcome.err != nil {
				t.Fatal(outcome.err)
			}
			result := outcome.result
			want := extension.ExecResult{Killed: true, Truncated: true}
			if overflowStream == "stdout" {
				want.Stdout = "a"
			} else {
				want.Stderr = "a"
			}
			want.Code = result.Code
			if result != want || result.Code == 0 {
				t.Fatalf("result = %+v, want %q on %s only, killed, truncated and a nonzero code", result, "a", overflowStream)
			}
		})
	}
}
