//go:build darwin

package nativeplatform

import (
	"runtime"
	"sync"
	"unsafe"
)

// Pig builds with CGO_ENABLED=0, so it reaches CoreGraphics the way
// golang.org/x/sys/unix reaches libSystem: dlopen and dlsym are imported from
// libSystem through assembly trampolines and called with syscall.syscall.
// CoreGraphics itself is loaded lazily, on the first modifier query, as Pi
// loads its N-API helper on demand.

//go:cgo_import_dynamic libc_dlopen dlopen "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_dlsym dlsym "/usr/lib/libSystem.B.dylib"

var (
	libc_dlopen_trampoline_addr uintptr
	libc_dlsym_trampoline_addr  uintptr
)

//go:linkname syscall_syscall syscall.syscall
func syscall_syscall(fn, a1, a2, a3 uintptr) (r1, r2, err uintptr)

const (
	rtldLazy  = 0x1
	rtldLocal = 0x4
	// kCGEventSourceStateCombinedSessionState.
	cgEventSourceStateCombinedSessionState = 0
	coreGraphicsPath                       = "/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics"
)

var (
	cgEventSourceFlagsStateOnce sync.Once
	cgEventSourceFlagsState     uintptr
)

func cString(s string) []byte { return append([]byte(s), 0) }

func loadCGEventSourceFlagsState() uintptr {
	cgEventSourceFlagsStateOnce.Do(func() {
		path := cString(coreGraphicsPath)
		handle, _, _ := syscall_syscall(libc_dlopen_trampoline_addr, uintptr(unsafe.Pointer(&path[0])), rtldLazy|rtldLocal, 0) //nolint:gosec // G103: dlopen takes a C string; path stays alive until KeepAlive.
		runtime.KeepAlive(path)
		if handle == 0 {
			return
		}
		name := cString("CGEventSourceFlagsState")
		fn, _, _ := syscall_syscall(libc_dlsym_trampoline_addr, handle, uintptr(unsafe.Pointer(&name[0])), 0) //nolint:gosec // G103: dlsym takes a C string; name stays alive until KeepAlive.
		runtime.KeepAlive(name)
		cgEventSourceFlagsState = fn
	})
	return cgEventSourceFlagsState
}

// modifierMaskForName mirrors darwin-platform.m modifier_mask_for_name.
func modifierMaskForName(name string) uint64 {
	switch name {
	case "shift":
		return 0x00020000 // kCGEventFlagMaskShift
	case "command":
		return 0x00100000 // kCGEventFlagMaskCommand
	case "control":
		return 0x00040000 // kCGEventFlagMaskControl
	case "option":
		return 0x00080000 // kCGEventFlagMaskAlternate
	}
	return 0
}

// isModifierPressed mirrors darwin-platform.m is_modifier_pressed.
func isModifierPressed(name string) bool {
	mask := modifierMaskForName(name)
	if mask == 0 {
		return false
	}
	fn := loadCGEventSourceFlagsState()
	if fn == 0 {
		return false
	}
	flags, _, _ := syscall_syscall(fn, cgEventSourceStateCombinedSessionState, 0, 0)
	return uint64(flags)&mask != 0
}
