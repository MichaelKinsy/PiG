//go:build windows

package runtimecell

// renameRetryable reports whether err is a transient lock worth retrying.
func renameRetryable(error) bool { return false }
