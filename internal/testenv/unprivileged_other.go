//go:build !unix && !windows

package testenv

import "testing"

// runUnprivileged continues in this process: outside Unix, no process identity bypasses the permission fixtures.
func runUnprivileged(*testing.T) bool { return false }
