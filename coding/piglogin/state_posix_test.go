//go:build !windows

package piglogin_test

import "io/fs"

func onlyOwner(mode fs.FileMode) bool { return mode == 0o600 }
