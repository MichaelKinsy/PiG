package codingagent

import (
	"fmt"
	"os/exec"
	"runtime/debug"
	"testing"
	"time"
)

// Pi packages/coding-agent/src/utils/clipboard-image.ts:106,191 shares packages/coding-agent/src/utils/clipboard-command.ts:37's 50 MiB stdout bound. Exercise the production image-command path, not only the separate clipboard-copy helper.
func TestClipboardImageCommandEnforcesDefaultBufferLimit(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("clipboard command tests require Node: ", err)
	}
	previous := clipboardRun
	clipboardRun = defaultClipboardRunner
	t.Cleanup(func() { clipboardRun = previous })
	const limit = 50 * 1024 * 1024 // packages/coding-agent/src/utils/clipboard-command.ts:37
	// The bound is under test, not the deadline: node's start and a 50 MiB read through a race build exceed the 3 s Pi callers use once the host is loaded (signal: killed), so use the generous deadline TestRunClipboardCommand uses.
	const slow = 30 * time.Second
	for _, size := range []int{0, 16, limit, limit + 1} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			// A 50 MiB read is held about twice while it is joined. Return each case's garbage to the OS before the next one starts, so the resident set stays at one case instead of the sum of the two large ones (which the parallel packages of a full test run cannot afford under memory pressure).
			t.Cleanup(debug.FreeOSMemory)
			out, err := runClipboardImageCommandContext(t.Context(), slow, node, "-e", fmt.Sprintf("process.stdout.write(Buffer.alloc(%d))", size))
			if size > limit {
				if err == nil || len(out) != 0 {
					t.Fatalf("image command accepted %d bytes beyond the %d-byte limit: returned=%d err=%v", size, limit, len(out), err)
				}
				return
			}
			if err != nil || len(out) != size {
				t.Fatalf("image command output=%d err=%v, want %d-byte success", len(out), err, size)
			}
		})
	}
}
