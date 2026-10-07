//go:build darwin

package nativeplatform

// Ports packages/tui/src/native-platform.ts

import (
	"context"
	"errors"
	"runtime"
	"slices"
	"sync"
	"unsafe"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

//go:linkname syscall_syscall6 syscall.syscall6
func syscall_syscall6(fn, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2, err uintptr)

type darwinClipboardSymbols struct {
	message, class, selector, pushPool, popPool uintptr
	text, png, tiff                             uintptr
	fileURLsOnlyKey                             uintptr
}

var loadDarwinClipboard = sync.OnceValue(func() *darwinClipboardSymbols {
	path := cString("/System/Library/Frameworks/AppKit.framework/AppKit")
	handle, _, _ := syscall_syscall(libc_dlopen_trampoline_addr, uintptr(unsafe.Pointer(&path[0])), rtldLazy|rtldLocal, 0) //nolint:gosec // G103: fixed system framework path stays live through KeepAlive; no CLI-controlled library is loaded.
	runtime.KeepAlive(path)
	if handle == 0 {
		return nil
	}
	symbol := func(name string) uintptr {
		bytes := cString(name)
		address, _, _ := syscall_syscall(libc_dlsym_trampoline_addr, handle, uintptr(unsafe.Pointer(&bytes[0])), 0) //nolint:gosec // G103: fixed symbol names are kept alive for dlsym on the system framework.
		runtime.KeepAlive(bytes)
		return address
	}
	value := func(name string) uintptr {
		address := symbol(name)
		if address == 0 {
			return 0
		}
		return **(**uintptr)(unsafe.Pointer(&address)) //nolint:gosec // G103: dlsym supplies an AppKit-owned pointer constant, not an address from CLI or clipboard data.
	}
	symbols := &darwinClipboardSymbols{message: symbol("objc_msgSend"), class: symbol("objc_getClass"), selector: symbol("sel_registerName"), pushPool: symbol("objc_autoreleasePoolPush"), popPool: symbol("objc_autoreleasePoolPop"), text: value("NSPasteboardTypeString"), png: value("NSPasteboardTypePNG"), tiff: value("NSPasteboardTypeTIFF"), fileURLsOnlyKey: value("NSPasteboardURLReadingFileURLsOnlyKey")}
	if symbols.message == 0 || symbols.class == 0 || symbols.selector == 0 || symbols.pushPool == 0 || symbols.popPool == 0 || symbols.text == 0 || symbols.png == 0 || symbols.tiff == 0 {
		return nil
	}
	return symbols
})

func (s *darwinClipboardSymbols) named(function uintptr, name string) uintptr {
	bytes := cString(name)
	value, _, _ := syscall_syscall(function, uintptr(unsafe.Pointer(&bytes[0])), 0, 0) //nolint:gosec // G103: selector/class names are implementation constants and remain live through KeepAlive.
	runtime.KeepAlive(bytes)
	return value
}
func (s *darwinClipboardSymbols) send(object uintptr, selector string, a, b, c uintptr) uintptr {
	value, _, _ := syscall_syscall6(s.message, object, s.named(s.selector, selector), a, b, c, 0)
	return value
}

// sendPointer is send for a first argument that points into Go memory, which send must never receive as a uintptr: the goroutine stack can move while send resolves the selector, and a uintptr would still hold the old address. Here the argument stays a pointer, which the runtime updates when the stack moves, until the system call expression converts it, which keeps the memory alive and in place until the call returns.
func (s *darwinClipboardSymbols) sendPointer(object uintptr, selector string, a unsafe.Pointer, b, c uintptr) uintptr {
	name := s.named(s.selector, selector)
	value, _, _ := syscall_syscall6(s.message, object, name, uintptr(a), b, c, 0)
	return value
}
func (s *darwinClipboardSymbols) typeNamed(name string) uintptr { return s.named(s.class, name) }

func openDarwinClipboard(ctx context.Context) (*darwinClipboardSymbols, uintptr, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, nil, err
	}
	s := loadDarwinClipboard()
	if s == nil {
		return nil, 0, nil, errors.New("Native clipboard unavailable")
	}
	runtime.LockOSThread()
	pool, _, _ := syscall_syscall(s.pushPool, 0, 0, 0)
	cleanup := func() { _, _, _ = syscall_syscall(s.popPool, pool, 0, 0); runtime.UnlockOSThread() }
	pasteboard := s.send(s.typeNamed("NSPasteboard"), "generalPasteboard", 0, 0, 0)
	if pasteboard == 0 {
		cleanup()
		return nil, 0, nil, errors.New("Could not open clipboard")
	}
	return s, pasteboard, cleanup, nil
}

func copyDarwinBytes(address, length uintptr) ([]byte, error) {
	if length > uintptr(int(^uint(0)>>1)) || address == 0 && length != 0 {
		return nil, errors.New("Could not create clipboard value")
	}
	if length == 0 {
		return []byte{}, nil
	}
	data := unsafe.Slice(*(**byte)(unsafe.Pointer(&address)), int(length)) //nolint:gosec // G103: AppKit provides the pointer and length; the object remains inside its same-thread autorelease pool through this bounded copy.
	return slices.Clone(data), nil
}

func readDarwinClipboardText(ctx context.Context) (*string, bool, error) {
	s, board, cleanup, err := openDarwinClipboard(ctx)
	if err != nil {
		return nil, true, err
	}
	defer cleanup()
	text, err := s.pasteboardText(board)
	return text, true, err
}

// pasteboardText runs inside the caller's autorelease pool, which owns every object it creates.
func (s *darwinClipboardSymbols) pasteboardText(board uintptr) (*string, error) {
	text := s.send(board, "stringForType:", s.text, 0, 0)
	if text == 0 {
		return nil, nil
	}
	address := s.send(text, "UTF8String", 0, 0, 0)
	if address == 0 {
		return nil, errors.New("Could not encode clipboard text")
	}
	length := s.send(text, "lengthOfBytesUsingEncoding:", 4, 0, 0)
	data, err := copyDarwinBytes(address, length)
	if err != nil {
		return nil, err
	}
	value := string(data)
	return &value, nil
}

func readDarwinClipboardImage(ctx context.Context) ([]byte, bool, error) {
	s, board, cleanup, err := openDarwinClipboard(ctx)
	if err != nil {
		return nil, true, err
	}
	defer cleanup()
	data, err := s.pasteboardImage(board)
	return data, true, err
}

// pasteboardImage runs inside the caller's autorelease pool, which owns every object it creates. A pasteboard without a PNG or TIFF type is a nil result.
func (s *darwinClipboardSymbols) pasteboardImage(board uintptr) ([]byte, error) {
	types := [2]uintptr{s.png, s.tiff}
	array := s.sendPointer(s.typeNamed("NSArray"), "arrayWithObjects:count:", unsafe.Pointer(&types[0]), 2, 0) //nolint:gosec // G103: NSArray copies the two AppKit-owned type objects synchronously; sendPointer keeps the Go array alive and in place for the call.
	if s.send(board, "availableTypeFromArray:", array, 0, 0) == 0 {
		return nil, nil
	}
	png := s.send(board, "dataForType:", s.png, 0, 0)
	if png == 0 {
		image := s.send(s.typeNamed("NSImage"), "alloc", 0, 0, 0)
		image = s.send(image, "initWithPasteboard:", board, 0, 0)
		if image != 0 {
			s.send(image, "autorelease", 0, 0, 0)
		}
		tiff := s.send(image, "TIFFRepresentation", 0, 0, 0)
		if tiff != 0 {
			bitmap := s.send(s.typeNamed("NSBitmapImageRep"), "imageRepWithData:", tiff, 0, 0)
			if bitmap != 0 {
				properties := s.send(s.typeNamed("NSDictionary"), "dictionary", 0, 0, 0)
				png = s.send(bitmap, "representationUsingType:properties:", 4, properties, 0)
			}
		}
	}
	if png == 0 {
		return nil, errors.New("Clipboard does not contain an image")
	}
	return copyDarwinBytes(s.send(png, "bytes", 0, 0, 0), s.send(png, "length", 0, 0, 0))
}

// readDarwinFilePaths reads the file URLs on the general pasteboard as POSIX paths. Mirrors darwin-platform.m's CLIPBOARD_FILES branch: `readObjectsForClasses:options:` with NSPasteboardURLReadingFileURLsOnlyKey, then each URL's fileSystemRepresentation. No file URLs is a nil result with available=true.
func readDarwinFilePaths(ctx context.Context) ([]string, bool, error) {
	s, board, cleanup, err := openDarwinClipboard(ctx)
	if err != nil {
		return nil, true, err
	}
	defer cleanup()
	if s.fileURLsOnlyKey == 0 {
		return nil, false, nil
	}
	paths, err := s.pasteboardFilePaths(board)
	return paths, true, err
}

// pasteboardFilePaths runs inside the caller's autorelease pool, which owns every object it creates.
func (s *darwinClipboardSymbols) pasteboardFilePaths(board uintptr) ([]string, error) {
	urlClass := [1]uintptr{s.typeNamed("NSURL")}
	classes := s.sendPointer(s.typeNamed("NSArray"), "arrayWithObjects:count:", unsafe.Pointer(&urlClass[0]), 1, 0) //nolint:gosec // G103: NSArray copies the one AppKit-owned class object synchronously; sendPointer keeps the Go array alive and in place for the call.
	yes := s.send(s.typeNamed("NSNumber"), "numberWithBool:", 1, 0, 0)
	options := s.send(s.typeNamed("NSDictionary"), "dictionaryWithObject:forKey:", yes, s.fileURLsOnlyKey, 0)
	urls := s.send(board, "readObjectsForClasses:options:", classes, options, 0)
	if urls == 0 {
		return nil, nil
	}
	count := s.send(urls, "count", 0, 0, 0)
	var paths []string
	for index := range count {
		url := s.send(urls, "objectAtIndex:", index, 0, 0)
		if url == 0 {
			continue
		}
		address := s.send(url, "fileSystemRepresentation", 0, 0, 0)
		if address == 0 {
			continue
		}
		path, err := copyDarwinCString(address)
		if err != nil {
			return nil, err
		}
		paths = append(paths, jsstring.FromUTF8(path))
	}
	return paths, nil
}

// copyDarwinCString copies the NUL-terminated bytes at address, which AppKit owns until the caller's autorelease pool drains.
func copyDarwinCString(address uintptr) ([]byte, error) {
	if address == 0 {
		return nil, errors.New("Could not create clipboard value")
	}
	base := *(**byte)(unsafe.Pointer(&address)) //nolint:gosec // G103: AppKit provides this C string and it lives through the same-thread pool; the scan below stops at its terminator.
	var length int
	for *(*byte)(unsafe.Add(unsafe.Pointer(base), length)) != 0 { //nolint:gosec // G103: reads the bytes of the NUL-terminated string AppKit returned, one at a time, up to its terminator.
		length++
	}
	return slices.Clone(unsafe.Slice(base, length)), nil //nolint:gosec // G103: the scan above bounded length by the terminator.
}

func writeDarwinClipboardText(ctx context.Context, text string) error {
	s, board, cleanup, err := openDarwinClipboard(ctx)
	if err != nil {
		return err
	}
	defer cleanup()
	return s.setPasteboardText(board, text)
}

// setPasteboardText runs inside the caller's autorelease pool, which owns every object it creates.
func (s *darwinClipboardSymbols) setPasteboardText(board uintptr, text string) error {
	data := jsstring.ToUTF8(text)
	length := len(data)
	data = append(data, 0)
	value := s.send(s.typeNamed("NSString"), "alloc", 0, 0, 0)
	value = s.sendPointer(value, "initWithBytes:length:encoding:", unsafe.Pointer(&data[0]), uintptr(length), 4) //nolint:gosec // G103: NSString copies this owned UTF-8 buffer synchronously; sendPointer keeps it alive and in place for the call.
	if value == 0 {
		return errors.New("Clipboard text is not valid UTF-8")
	}
	s.send(value, "autorelease", 0, 0, 0)
	s.send(board, "clearContents", 0, 0, 0)
	if s.send(board, "setString:forType:", value, s.text, 0) == 0 {
		return errors.New("Could not set clipboard text")
	}
	return nil
}

var darwinClipboardAPI = NativeClipboard{GetText: readDarwinClipboardText, GetImage: readDarwinClipboardImage, GetFilePaths: readDarwinFilePaths, SetText: writeDarwinClipboardText, IsModifierPressed: IsModifierPressed}

func platformClipboard() *NativeClipboard {
	if loadDarwinClipboard() == nil {
		return nil
	}
	return &darwinClipboardAPI
}
