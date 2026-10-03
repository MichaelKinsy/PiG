//go:build windows

package piglogin_test

import "io/fs"

// Windows reports no POSIX permission bits.
func onlyOwner(fs.FileMode) bool { return true }
