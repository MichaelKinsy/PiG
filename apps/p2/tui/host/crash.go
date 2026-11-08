package host

// A runtime that panics ends the client the way a crashed worker did: the failure is reported once with a sanitized message and stack, and the terminal's
// modes are undone like on any exit. Pi's sandbox reported "TUI sandbox crashed with an uncaught exception" / "message handler threw" (a TypeError stack with
// control characters stripped and no data: URLs); the runtime runs in this process, so the panic is caught on its owner loop.
//
// pig divergence (D611): a crash is caught on the owner loop of the shared process, not as a worker's exit; see docs/parity/divergences/D611.md.
//
// Go-only: pi: apps/p2/src/hosts/tui-sandbox.ts (D611).

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
)

// crashMessage is what the visitor is told of a panic: its value and the frames above the panic site, without control characters, file paths or bulk.
func crashMessage(value any) string {
	var stack strings.Builder
	pcs := make([]uintptr, 32)
	frames := runtime.CallersFrames(pcs[:runtime.Callers(0, pcs)])
	panicked, shown := false, 0
	for shown < 8 {
		frame, more := frames.Next()
		if panicked && !strings.HasPrefix(frame.Function, "runtime.") {
			fmt.Fprintf(&stack, "\n    at %s (%s:%d)", frame.Function, filepath.Base(frame.File), frame.Line)
			shown++
		}
		if strings.HasPrefix(frame.Function, "runtime.gopanic") {
			panicked = true
		}
		if !more {
			break
		}
	}
	return jsSlice(stripControls("TUI crashed with a panic:\n"+fmt.Sprint(value)+stack.String()), 2000)
}

// stripControls drops every control character but the newline (an escape sequence in a panic value never reaches the terminal).
func stripControls(text string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || (r >= 0x20 && r != 0x7f && (r < 0x80 || r > 0x9f)) {
			return r
		}
		return -1
	}, text)
}

// run is one step of the owner loop: a panic ends the client with its crash message.
func (c *Client) run(fn func()) {
	defer func() {
		if value := recover(); value != nil && c.crashed.CompareAndSwap(false, true) {
			c.fail(crashMessage(value))
		}
	}()
	fn()
}
