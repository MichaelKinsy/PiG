//go:build windows

package codingagent

import "os/exec"

// exitSignalName is "" on Windows: a terminated process reports an exit code, as Node's close event does.
func exitSignalName(*exec.ExitError) string { return "" }
