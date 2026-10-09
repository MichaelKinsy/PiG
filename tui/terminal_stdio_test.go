package tui

import (
	"os"
	"testing"
)

// upstream: packages/tui/src/terminal.ts ProcessTerminal: `new ProcessTerminal()` takes no arguments and drives the process's own standard
// input and output; the shared terminal of the package is built that way.
func TestNewStdioProcessTerminalBindsTheProcessStreams(t *testing.T) {
	term := NewStdioProcessTerminal()
	if term.stdin != os.Stdin || term.stdout != os.Stdout || term.out != os.Stdout {
		t.Fatalf("terminal streams = %v/%v/%v, want the process's stdin and stdout", term.stdin, term.stdout, term.out)
	}
	if processTerminal.stdin != os.Stdin || processTerminal.stdout != os.Stdout {
		t.Fatal("the package's shared terminal is not bound to the process streams")
	}
}
