//go:build windows

package p2test

// stacksEnd is the line a worker writes to stderr after the stacks of its goroutines (Windows has no such signal: none).
const stacksEnd = "p2test: the stacks of the goroutines end"

// dumpStacksOnSignal does nothing: a process on Windows cannot be signalled for its stacks.
func dumpStacksOnSignal() {}

// askForStacks is false: a process on Windows cannot be signalled for its stacks.
func askForStacks(int) bool { return false }
