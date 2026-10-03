//go:build windows

package codingagent

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// loadedSharedObjects lists the images loaded into this process, the running
// executable first, as upstream reads them from process.report.
func loadedSharedObjects() []string {
	process := windows.CurrentProcess()
	modules := make([]windows.Handle, 64)
	for {
		var needed uint32
		size := uint32(len(modules)) * uint32(unsafe.Sizeof(modules[0]))
		if err := windows.EnumProcessModules(process, &modules[0], size, &needed); err != nil {
			return nil
		}
		count := int(needed / uint32(unsafe.Sizeof(modules[0])))
		if count <= len(modules) {
			modules = modules[:count]
			break
		}
		modules = make([]windows.Handle, count)
	}
	name := make([]uint16, windows.MAX_LONG_PATH)
	paths := make([]string, 0, len(modules))
	for _, module := range modules {
		n, err := windows.GetModuleFileName(module, &name[0], uint32(len(name)))
		if err != nil || n == 0 {
			continue
		}
		paths = append(paths, longWindowsPath(windows.UTF16ToString(name[:n])))
	}
	return paths
}

// longWindowsPath expands 8.3 short names. A process started through a short path (C:\Users\RUNNER~1\...) reports
// its modules under that path while os.Executable names the long one, and the two must compare equal to find the
// running image in its package directory.
func longWindowsPath(path string) string {
	short, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return path
	}
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n, err := windows.GetLongPathName(short, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 || int(n) > len(buf) {
		return path
	}
	return windows.UTF16ToString(buf[:n])
}
