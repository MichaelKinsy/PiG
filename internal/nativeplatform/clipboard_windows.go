//go:build windows

package nativeplatform

// Ports packages/tui/src/native-platform.ts

import (
	"context"
	"encoding/binary"
	"errors"
	"runtime"
	"slices"
	"sync"
	"time"
	"unsafe"

	"github.com/MichaelKinsy/PiG/internal/jsstring"

	"golang.org/x/sys/windows"
)

var (
	clipboardUser32            = windows.NewLazySystemDLL("user32.dll")
	clipboardKernel32          = windows.NewLazySystemDLL("kernel32.dll")
	openClipboard              = clipboardUser32.NewProc("OpenClipboard")
	closeClipboard             = clipboardUser32.NewProc("CloseClipboard")
	registerClipboardFormat    = clipboardUser32.NewProc("RegisterClipboardFormatW")
	isClipboardFormatAvailable = clipboardUser32.NewProc("IsClipboardFormatAvailable")
	getClipboardData           = clipboardUser32.NewProc("GetClipboardData")
	clipboardGlobalLock        = clipboardKernel32.NewProc("GlobalLock")
	clipboardGlobalUnlock      = clipboardKernel32.NewProc("GlobalUnlock")
	clipboardGlobalSize        = clipboardKernel32.NewProc("GlobalSize")
	clipboardGlobalAlloc       = clipboardKernel32.NewProc("GlobalAlloc")
	clipboardGlobalFree        = clipboardKernel32.NewProc("GlobalFree")
	clipboardCreateWindow      = clipboardUser32.NewProc("CreateWindowExW")
	clipboardDestroyWindow     = clipboardUser32.NewProc("DestroyWindow")
	clipboardEmpty             = clipboardUser32.NewProc("EmptyClipboard")
	clipboardSetData           = clipboardUser32.NewProc("SetClipboardData")
	clipboardModuleHandle      = clipboardKernel32.NewProc("GetModuleHandleW")
	clipboardImageAvailable    = sync.OnceValue(func() bool {
		for _, proc := range []*windows.LazyProc{openClipboard, closeClipboard, registerClipboardFormat, isClipboardFormatAvailable, getClipboardData, clipboardGlobalLock, clipboardGlobalUnlock, clipboardGlobalSize} {
			if proc.Find() != nil {
				return false
			}
		}
		return true
	})
)

// ReadClipboardImage reads native PNG or DIB clipboard data without invoking command tools. DIB data is returned as a BMP, for the shared image normalizer to convert to PNG.
func ReadClipboardImage(ctx context.Context) ([]byte, error) {
	if !clipboardImageAvailable() {
		return nil, nil
	}
	cleanup, err := lockWindowsClipboard(ctx, false)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	name := windows.StringToUTF16Ptr("PNG")
	pngFormat, _, _ := registerClipboardFormat.Call(uintptr(unsafe.Pointer(name))) //nolint:gosec // G103: the fixed UTF-16 format name remains live through KeepAlive; no CLI-supplied pointer reaches Windows.
	runtime.KeepAlive(name)
	available := func(format uintptr) bool { r, _, _ := isClipboardFormatAvailable.Call(format); return r != 0 }
	const dibV5 = 17
	const dib = 8
	hasPNG := pngFormat != 0 && available(pngFormat)
	if !hasPNG && !available(dibV5) && !available(dib) {
		return nil, nil
	}
	var handle uintptr
	if hasPNG {
		handle, _, _ = getClipboardData.Call(pngFormat)
	}
	isPNG := handle != 0
	if handle == 0 && available(dibV5) {
		handle, _, _ = getClipboardData.Call(dibV5)
	}
	if handle == 0 && available(dib) {
		handle, _, _ = getClipboardData.Call(dib)
	}
	if handle == 0 {
		return nil, errors.New("Clipboard does not contain an image")
	}
	address, _, _ := clipboardGlobalLock.Call(handle)
	if address != 0 {
		defer func() { _, _, _ = clipboardGlobalUnlock.Call(handle) }()
	}
	size, _, _ := clipboardGlobalSize.Call(handle)
	if address == 0 || size == 0 || size > uintptr(int(^uint(0)>>1)) {
		return nil, errors.New("Clipboard does not contain an image")
	}
	// GlobalLock owns this OS allocation until GlobalUnlock. Reinterpret the returned address word without treating it as Go-heap memory; GlobalSize bounds every read.
	pointer := *(**byte)(unsafe.Pointer(&address)) //nolint:gosec // G103: GlobalLock supplies the OS address, not clipboard content or CLI input; deferred GlobalUnlock owns its lifetime.
	contents := unsafe.Slice(pointer, int(size))   //nolint:gosec // G103: GlobalSize bounds this locked OS allocation and size is checked against the Go slice limit above.
	if isPNG {
		return slices.Clone(contents), nil
	}
	bitmap := bitmapFromDIB(contents)
	if bitmap == nil {
		return nil, errors.New("Could not create clipboard image buffer")
	}
	return bitmap, nil
}

// lockWindowsClipboard keeps the owner window, clipboard lock, and native calls on one OS thread until cleanup.
func lockWindowsClipboard(ctx context.Context, write bool) (func(), error) {
	runtime.LockOSThread()
	var owner uintptr
	unlock := func() {
		if owner != 0 {
			_, _, _ = clipboardDestroyWindow.Call(owner)
		}
		runtime.UnlockOSThread()
	}
	if write {
		class := windows.StringToUTF16Ptr("STATIC")
		module, _, _ := clipboardModuleHandle.Call(0)
		owner, _, _ = clipboardCreateWindow.Call(0, uintptr(unsafe.Pointer(class)), 0, 0, 0, 0, 0, 0, ^uintptr(2), 0, module, 0) //nolint:gosec // G103: fixed class name stays live through KeepAlive; no CLI-supplied pointer reaches Windows.
		runtime.KeepAlive(class)
		if owner == 0 {
			unlock()
			return nil, errors.New("Could not create clipboard owner window")
		}
	}
	for range 10 {
		if err := ctx.Err(); err != nil {
			unlock()
			return nil, err
		}
		if result, _, _ := openClipboard.Call(owner); result != 0 {
			return func() { _, _, _ = closeClipboard.Call(); unlock() }, nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	unlock()
	return nil, errors.New("Could not open clipboard")
}

func readWindowsClipboardText(ctx context.Context) (*string, bool, error) {
	cleanup, err := lockWindowsClipboard(ctx, false)
	if err != nil {
		return nil, true, err
	}
	defer cleanup()
	if available, _, _ := isClipboardFormatAvailable.Call(13); available == 0 {
		return nil, true, nil
	}
	handle, _, _ := getClipboardData.Call(13)
	if handle == 0 {
		return nil, true, errors.New("Clipboard does not contain text")
	}
	address, _, _ := clipboardGlobalLock.Call(handle)
	if address == 0 {
		return nil, true, errors.New("Clipboard does not contain text")
	}
	defer func() { _, _, _ = clipboardGlobalUnlock.Call(handle) }()
	size, _, _ := clipboardGlobalSize.Call(handle)
	if size > uintptr(int(^uint(0)>>1)) {
		return nil, true, errors.New("Clipboard does not contain text")
	}
	data := unsafe.Slice(*(**byte)(unsafe.Pointer(&address)), int(size)) //nolint:gosec // G103: GlobalLock supplies OS memory; GlobalSize bounds the read until deferred unlock.
	units := make([]uint16, 0, len(data)/2)
	for offset := 0; offset+1 < len(data); offset += 2 {
		unit := binary.LittleEndian.Uint16(data[offset:])
		if unit == 0 {
			break
		}
		units = append(units, unit)
	}
	text := jsstring.FromUTF16(units)
	return &text, true, nil
}

func writeWindowsClipboardText(ctx context.Context, text string) error {
	cleanup, err := lockWindowsClipboard(ctx, true)
	if err != nil {
		return err
	}
	defer cleanup()
	units := append(jsstring.ToUTF16(text), 0)
	if len(units) > int(^uint(0)>>1)/2 {
		return errors.New("Could not set clipboard text")
	}
	size := len(units) * 2
	handle, _, _ := clipboardGlobalAlloc.Call(2, uintptr(size))
	if handle == 0 {
		return errors.New("Could not set clipboard text")
	}
	transferred := false
	defer func() {
		if !transferred {
			_, _, _ = clipboardGlobalFree.Call(handle)
		}
	}()
	address, _, _ := clipboardGlobalLock.Call(handle)
	if address == 0 {
		return errors.New("Could not set clipboard text")
	}
	data := unsafe.Slice(*(**byte)(unsafe.Pointer(&address)), size) //nolint:gosec // G103: this operation allocated exactly size bytes; the lock is checked before the bounded copy.
	for index, unit := range units {
		binary.LittleEndian.PutUint16(data[index*2:], unit)
	}
	_, _, _ = clipboardGlobalUnlock.Call(handle)
	if result, _, _ := clipboardEmpty.Call(); result == 0 {
		return errors.New("Could not set clipboard text")
	}
	if result, _, _ := clipboardSetData.Call(13, handle); result == 0 {
		return errors.New("Could not set clipboard text")
	}
	transferred = true
	return nil
}

var windowsClipboardAPI = NativeClipboard{
	GetText: readWindowsClipboardText,
	GetImage: func(ctx context.Context) ([]byte, bool, error) {
		data, err := ReadClipboardImage(ctx)
		return data, true, err
	},
	SetText:           writeWindowsClipboardText,
	IsModifierPressed: IsModifierPressed,
	EnableVirtualTerminalInput: func() bool {
		var mode uint32
		return windows.GetConsoleMode(windows.Stdin, &mode) == nil && windows.SetConsoleMode(windows.Stdin, mode|0x0200) == nil
	},
}

var windowsClipboardAvailable = sync.OnceValue(func() bool {
	if !clipboardImageAvailable() {
		return false
	}
	for _, proc := range []*windows.LazyProc{clipboardGlobalAlloc, clipboardGlobalFree, clipboardCreateWindow, clipboardDestroyWindow, clipboardEmpty, clipboardSetData, clipboardModuleHandle} {
		if proc.Find() != nil {
			return false
		}
	}
	return true
})

func platformClipboard() *NativeClipboard {
	if !windowsClipboardAvailable() {
		return nil
	}
	return &windowsClipboardAPI
}
