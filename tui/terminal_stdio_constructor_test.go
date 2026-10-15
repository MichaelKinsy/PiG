package tui

import (
	"os"
	"testing"
)

// terminal.ts `new ProcessTerminal()` takes no arguments: it reads process.stdin and writes process.stdout.
func TestNewStdioProcessTerminalUsesTheProcessStreams(t *testing.T) {
	terminal := NewStdioProcessTerminal()
	if terminal.stdin != os.Stdin || terminal.stdout != os.Stdout {
		t.Fatalf("stdin = %v, stdout = %v, want the process's own", terminal.stdin, terminal.stdout)
	}
	if terminal.out != os.Stdout {
		t.Fatalf("control bytes go to %v, want process stdout", terminal.out)
	}
}
