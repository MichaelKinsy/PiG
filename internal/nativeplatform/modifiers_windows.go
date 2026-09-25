//go:build windows

package nativeplatform

import (
	"sync"

	"golang.org/x/sys/windows"
)

const keyPressedMask = 0x8000

var (
	getAsyncKeyStateOnce sync.Once
	getAsyncKeyState     *windows.LazyProc
)

func loadGetAsyncKeyState() *windows.LazyProc {
	getAsyncKeyStateOnce.Do(func() {
		proc := windows.NewLazySystemDLL("user32.dll").NewProc("GetAsyncKeyState")
		if proc.Find() == nil {
			getAsyncKeyState = proc
		}
	})
	return getAsyncKeyState
}

// isKeyPressed mirrors win32-platform.c is_key_pressed.
func isKeyPressed(proc *windows.LazyProc, virtualKey uintptr) bool {
	state, _, _ := proc.Call(virtualKey)
	return uint16(state)&keyPressedMask != 0
}

// isModifierPressed mirrors win32-platform.c is_modifier_name_pressed for the
// names upstream's ModifierKey type allows.
func isModifierPressed(name string) bool {
	var keys []uintptr
	switch name {
	case "shift":
		keys = []uintptr{0x10, 0xA0, 0xA1} // VK_SHIFT, VK_LSHIFT, VK_RSHIFT
	case "control":
		keys = []uintptr{0x11, 0xA2, 0xA3} // VK_CONTROL, VK_LCONTROL, VK_RCONTROL
	case "option":
		keys = []uintptr{0x12, 0xA4, 0xA5} // VK_MENU, VK_LMENU, VK_RMENU
	case "command":
		keys = []uintptr{0x5B, 0x5C} // VK_LWIN, VK_RWIN
	default:
		return false
	}
	proc := loadGetAsyncKeyState()
	if proc == nil {
		return false
	}
	for _, virtualKey := range keys {
		if isKeyPressed(proc, virtualKey) {
			return true
		}
	}
	return false
}
