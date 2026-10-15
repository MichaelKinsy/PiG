package codingagent

import (
	"bytes"
	"os/exec"
	"testing"
	"time"
)

// Ports packages/coding-agent/test/clipboard-command.test.ts:5-43 using the same Node children and byte vectors. Each case checks the deadline Pi requests, but the test, not the host's speed at starting Node, decides whether it fires.
func TestClipboardCommandUpstream(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("clipboard upstream tests require Node: ", err)
	}
	t.Run("preserves binary output and distinguishes empty success from failure", func(t *testing.T) {
		controlClipboardCommandDeadline(t, 3*time.Second)
		for _, tc := range []struct {
			script string
			want   []byte
			ok     bool
		}{
			{"process.stdout.write(Buffer.from([0, 255, 10]))", []byte{0, 255, 10}, true},
			{"", []byte{}, true},
			{"process.exit(1)", nil, false},
		} {
			out, ok := runClipboardCommand(node, []string{"-e", tc.script}, clipboardCommandOptions{})
			if ok != tc.ok || !bytes.Equal(out, tc.want) {
				t.Errorf("%q: output=%v ok=%v, want %v/%v", tc.script, out, ok, tc.want, tc.ok)
			}
		}
		if _, ok := runClipboardCommand("pi-clipboard-command-does-not-exist", nil, clipboardCommandOptions{}); ok {
			t.Fatal("missing executable succeeded")
		}
	})
	t.Run("sends Unicode input to clipboard writers", func(t *testing.T) {
		controlClipboardCommandDeadline(t, 3*time.Second)
		input := "café 日本語"
		script := "let text = ''; process.stdin.setEncoding('utf8'); process.stdin.on('data', c => text += c); process.stdin.on('end', () => process.exit(text === 'café 日本語' ? 0 : 1));"
		out, ok := runClipboardCommand(node, []string{"-e", script}, clipboardCommandOptions{input: &input})
		if !ok || len(out) != 0 {
			t.Fatalf("writer output=%v ok=%v", out, ok)
		}
	})
	t.Run("times out without blocking the event loop", func(t *testing.T) {
		fire := controlClipboardCommandDeadline(t, 200*time.Millisecond)
		done := make(chan bool, 1)
		go func() {
			_, ok := runClipboardCommand(node, []string{"-e", "setInterval(() => {}, 1000)"}, clipboardCommandOptions{timeout: 200 * time.Millisecond})
			done <- ok
		}()
		// The owner keeps ticking while the command is pending; the never-ending child returns only when its deadline fires.
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for ticks := 0; ticks <= 5; {
			select {
			case <-ticker.C:
				ticks++
			case ok := <-done:
				t.Fatalf("command returned ok=%v before its deadline fired", ok)
			}
		}
		fire()
		if <-done {
			t.Error("timed-out command succeeded")
		}
	})
	t.Run("rejects output above the buffer limit", func(t *testing.T) {
		controlClipboardCommandDeadline(t, 3*time.Second)
		if _, ok := runClipboardCommand(node, []string{"-e", "process.stdout.write(Buffer.alloc(1024))"}, clipboardCommandOptions{maxBytes: 16}); ok {
			t.Fatal("oversized output succeeded")
		}
	})
}
