//go:build !windows && !linux && !darwin

package experimental

import "testing"

// processExited has no process-state source on this platform, so kill(pid, 0) alone decides.
func processExited(*testing.T, int) bool { return false }
