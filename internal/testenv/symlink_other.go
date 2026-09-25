//go:build !windows

package testenv

// symlinkPrivilegeMissing reports false: creating a symbolic link needs no
// special privilege outside Windows.
func symlinkPrivilegeMissing(error) bool { return false }
