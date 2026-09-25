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
		paths = append(paths, windows.UTF16ToString(name[:n]))
	}
	return paths
}
