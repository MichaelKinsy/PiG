//go:build !windows

package codingagent

import (
	"context"
	"os"
	"testing"
)

// Pi ProcessTerminal.stop removes its input listener before editInExternalEditor inherits stdin. The stopped reader must not consume the child's input. A pipe waits like the terminal here; Windows interactive mode reads a console, which remote_editor_order_console_windows_test.go drives.
func TestInteractiveTerminalReaderHandsInputToExternalEditor(t *testing.T) {
	in, out, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = in.Close(); _ = out.Close() }()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	reader := newInteractiveTerminalReader(ctx, in)
	defer reader.pause()
	if _, err := out.WriteString("before"); err != nil {
		t.Fatal(err)
	}
	if got := string(<-reader.data); got != "before" {
		t.Fatalf("before=%q", got)
	}
	reader.pause()
	if _, err := out.WriteString("child"); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 5)
	if _, err := in.Read(buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "child" {
		t.Fatalf("child input=%q", buf)
	}
	reader.resume()
	if _, err := out.WriteString("after"); err != nil {
		t.Fatal(err)
	}
	if got := string(<-reader.data); got != "after" {
		t.Fatalf("after=%q", got)
	}
}
