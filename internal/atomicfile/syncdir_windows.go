//go:build windows

package atomicfile

// syncDirectory does nothing on Windows: a directory cannot be opened for FlushFileBuffers there (access denied), and MoveFileExW writes through. NOT VERIFIED BY RUN.
func syncDirectory(string) error { return nil }
