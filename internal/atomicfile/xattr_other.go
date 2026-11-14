//go:build !linux && !darwin

package atomicfile

import "os"

// carryAttributes has nothing to copy where the platform has no extended attributes the helper reads: Windows keeps the DACL, attributes and streams in ReplaceFileW, and the other Unix systems keep theirs as before.
func carryAttributes(*os.File, string) error { return nil }
