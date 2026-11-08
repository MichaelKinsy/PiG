//go:build !linux

package tsruntime

import "os"

// descriptorPath is "": only Linux names an open file as a path (/proc/self/fd) that execve takes, so the executable that runs here is
// the file at its path at exec time.
func descriptorPath(*os.File, os.FileInfo) string { return "" }
