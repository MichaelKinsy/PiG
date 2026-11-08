//go:build !unix

package tui

import "os"

// notifyResize has no signal on Windows: a resize is not delivered.
func notifyResize(chan<- os.Signal) {}

// ignoreInspectorSignal has nothing to do: Windows has no SIGUSR1.
func ignoreInspectorSignal() func() { return func() {} }
