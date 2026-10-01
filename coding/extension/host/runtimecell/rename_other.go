//go:build !windows

package runtimecell

// renameRetryable reports whether err is a transient lock worth retrying.
// Unix renames are not subject to the Windows sharing model.
func renameRetryable(error) bool { return false }
