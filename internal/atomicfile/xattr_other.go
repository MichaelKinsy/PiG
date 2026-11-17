//go:build !linux && !darwin && !windows

package atomicfile

import "os"

// carryAttributes has nothing to copy where the platform has no extended attributes the helper reads: the other Unix systems keep theirs as before. Windows
// carries the security descriptor (descriptor_windows.go).
func carryAttributes(*os.File, string) error { return nil }
