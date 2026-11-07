package tsruntime

import (
	"os"
	"runtime"
	"strconv"
)

// descriptorPath returns the path that starts the open file as a process, or "" when this platform or file cannot be started from a
// descriptor. The path is checked to be the same file as info: a /proc that is not mounted, or that shows another file, selects
// the path.
func descriptorPath(file *os.File, info os.FileInfo) string {
	// The Android launcher (linkerexec) starts programs through the system linker, which maps the file by path.
	if runtime.GOOS != "linux" {
		return ""
	}
	// A script starts through its interpreter, which opens the path it is given after exec has closed this descriptor.
	var head [2]byte
	if n, err := file.ReadAt(head[:], 0); n == len(head) && err == nil && string(head[:]) == "#!" {
		return ""
	}
	var fd uintptr
	conn, err := file.SyscallConn()
	if err != nil || conn.Control(func(f uintptr) { fd = f }) != nil {
		return ""
	}
	path := "/proc/self/fd/" + strconv.FormatUint(uint64(fd), 10)
	shown, err := os.Stat(path)
	if err != nil || !os.SameFile(shown, info) {
		return ""
	}
	return path
}
