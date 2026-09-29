package tui

// Ports packages/tui/src/terminal.ts:refreshTerminalDimensions.

// refreshTerminalDimensions makes the startup dimension refresh best-effort. The platform gate precedes signal delivery; the Windows watcher uses its console-size callback instead of a self-signal.
func refreshTerminalDimensions(windows bool, pid int, kill func(int) error) {
	if windows || pid <= 0 {
		return
	}
	_ = kill(pid)
}
