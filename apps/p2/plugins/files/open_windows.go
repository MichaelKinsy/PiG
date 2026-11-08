//go:build windows

package files

import "os"

func openFile(path string) (*os.File, error) { return os.OpenFile(path, os.O_RDONLY, 0) }
