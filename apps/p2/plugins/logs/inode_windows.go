//go:build windows

package logs

import "os"

func inodeOf(os.FileInfo) uint64 { return 0 }
