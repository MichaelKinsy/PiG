package codingagent

import (
	"fmt"
	"os/exec"
	"testing"
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
	for _, size := range []int{0, 16, limit, limit + 1} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			out, err := runClipboardImageCommandContext(t.Context(), clipboardCommandTimeout, node, "-e", fmt.Sprintf("process.stdout.write(Buffer.alloc(%d))", size))
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
