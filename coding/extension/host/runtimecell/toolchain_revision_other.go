//go:build !unix && !windows

package runtimecell

import (
	"fmt"
	"os"
)

func toolchainFileRevision(path string) (string, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return "", false
	}
	return fmt.Sprintf("%d:%d:%d", info.Size(), info.ModTime().UnixNano(), info.Mode()), true
}
