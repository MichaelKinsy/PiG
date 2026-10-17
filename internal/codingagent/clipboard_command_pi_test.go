package codingagent

import (
	"bytes"
	"io"
	"os"
	"testing"
	"time"
)

// pi: packages/coding-agent/src/utils/clipboard-command.ts

// Ports packages/coding-agent/test/clipboard-command.test.ts. The test binary re-executes itself as the clipboard helper so the cases need
// no external program. Each case checks the deadline Pi requests, but only the timeout case fires it, so the host's speed at starting the
// helper decides nothing.
func TestClipboardCommandHelper(t *testing.T) {
	switch os.Getenv("PIG_CLIPBOARD_HELPER") {
	case "":
		t.Skip("helper process only")
	case "binary":
		_, _ = os.Stdout.Write([]byte{0, 255, 10})
	case "empty":
	case "fail":
		os.Exit(1)
	case "unicode":
		b, _ := io.ReadAll(os.Stdin)
		if string(b) != "café 日本語" {
			os.Exit(1)
		}
	case "hang":
		time.Sleep(time.Minute)
	case "big":
		_, _ = os.Stdout.Write(make([]byte, 1024))
	}
	os.Exit(0)
}

func clipboardHelper(t *testing.T, mode string, options clipboardCommandOptions) ([]byte, bool) {
	t.Helper()
	t.Setenv("PIG_CLIPBOARD_HELPER", mode)
	return runClipboardCommand(os.Args[0], []string{"-test.run=^TestClipboardCommandHelper$"}, options)
}

func TestRunClipboardCommandMatchesPi(t *testing.T) {
	t.Run("preserves binary output and distinguishes empty success from failure", func(t *testing.T) {
		controlClipboardCommandDeadline(t, 3*time.Second)
		if out, ok := clipboardHelper(t, "binary", clipboardCommandOptions{}); !ok || !bytes.Equal(out, []byte{0, 255, 10}) {
			t.Fatalf("binary = %v, %v", out, ok)
		}
		if out, ok := clipboardHelper(t, "empty", clipboardCommandOptions{}); !ok || len(out) != 0 {
			t.Fatalf("empty success = %v, %v; want empty output with ok", out, ok)
		}
		if _, ok := clipboardHelper(t, "fail", clipboardCommandOptions{}); ok {
			t.Fatal("exit status 1 reported success")
		}
		if _, ok := runClipboardCommand("pig-clipboard-command-does-not-exist", nil, clipboardCommandOptions{}); ok {
			t.Fatal("missing command reported success")
		}
	})
	t.Run("sends Unicode input to clipboard writers", func(t *testing.T) {
		controlClipboardCommandDeadline(t, 3*time.Second)
		input := "café 日本語"
		if out, ok := clipboardHelper(t, "unicode", clipboardCommandOptions{input: &input}); !ok || len(out) != 0 {
			t.Fatalf("unicode writer = %v, %v", out, ok)
		}
	})
	t.Run("times out", func(t *testing.T) {
		fire := controlClipboardCommandDeadline(t, 200*time.Millisecond)
		t.Setenv("PIG_CLIPBOARD_HELPER", "hang")
		done := make(chan bool, 1)
		go func() {
			_, ok := runClipboardCommand(os.Args[0], []string{"-test.run=^TestClipboardCommandHelper$"}, clipboardCommandOptions{timeout: 200 * time.Millisecond})
			done <- ok
		}()
		// The helper sleeps for a minute, so only the deadline ends the command.
		fire()
		if <-done {
			t.Fatal("a hanging command reported success")
		}
	})
	t.Run("rejects output above the buffer limit", func(t *testing.T) {
		controlClipboardCommandDeadline(t, 3*time.Second)
		if _, ok := clipboardHelper(t, "big", clipboardCommandOptions{maxBytes: 16}); ok {
			t.Fatal("1024 bytes accepted under a 16-byte limit")
		}
	})
}
