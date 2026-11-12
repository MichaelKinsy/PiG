//go:build windows

package plugins

import "testing"

// pauseNode has no signal to stop the process with; the tests that need it skip on Windows.
func pauseNode(*testing.T, *NodeHost) (resume func()) { return func() {} }
