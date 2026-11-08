//go:build !unix

package app

import "io/fs"

// checkDirectoryOwner accepts any data directory: Node has no `process.getuid` on Windows, so Pi's check does not run there.
func checkDirectoryOwner(string, fs.FileInfo) error { return nil }
