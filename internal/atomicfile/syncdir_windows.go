//go:build windows

package atomicfile

// syncDirectory does nothing on Windows: a directory cannot be opened for FlushFileBuffers there (access denied), and the replacement is already as
// durable as the file system makes it (MoveFileEx writes through; ReplaceFileW does not support REPLACEFILE_WRITE_THROUGH). NOT VERIFIED BY RUN.
func syncDirectory(string) error { return nil }
