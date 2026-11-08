//go:build linux || darwin || freebsd

package node

import "os"

// openWalIndex opens the wal-index file at path for reading. These systems keep one page cache for a file whether it is
// mapped or read, so a read of the file sees what SQLite stored through its mapping of it as soon as the store happened.
func openWalIndex(path string) (*os.File, error) { return os.Open(path) }
