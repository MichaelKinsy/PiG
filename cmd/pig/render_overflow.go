package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"runtime/debug"

	"github.com/MichaelKinsy/PiG/tui"
)

// exitOnRenderOverflow reports a main-screen differential-render overflow
// raised outside interactive mode (startup prompts, the config selector),
// where upstream has no uncaughtException handler: Node prints the thrown
// Error and its stack to stderr and exits 1. The renderer already wrote its
// crash log and stopped before panicking. Any other panic continues.
func exitOnRenderOverflow() {
	value := recover()
	if value == nil {
		return
	}
	if !reportRenderOverflow(value, debug.Stack(), os.Stderr) {
		panic(value)
	}
	exitProcess(1)
}

func reportRenderOverflow(value any, stack []byte, stderr io.Writer) bool {
	var overflow *tui.RenderOverflowError
	err, ok := value.(error)
	if !ok || !errors.As(err, &overflow) {
		return false
	}
	_, _ = fmt.Fprintf(stderr, "Error: %s\n%s", overflow.Error(), stack)
	return true
}
