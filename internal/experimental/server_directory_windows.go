package experimental

import "os"

// Windows has no POSIX ownership; EnsurePrivateServerDirectory rejects it before checking directory metadata.
func serverDirectoryOwned(os.FileInfo) bool { return false }
