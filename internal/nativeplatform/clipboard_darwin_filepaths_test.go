//go:build darwin

package nativeplatform

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"unsafe"
)

// Reads file URLs from a unique named pasteboard, never the user's general one. Mirrors packages/tui/native/darwin/src/darwin-platform.m CLIPBOARD_FILES (upstream 0.99.1), The published prebuild is a macOS-only native module, so this test compiles on other hosts (GOOS=darwin go vet) but runs only on macOS.
func TestDarwinPasteboardFilePaths(t *testing.T) {
	s := loadDarwinClipboard()
	if s == nil || s.fileURLsOnlyKey == 0 {
		t.Skip("AppKit pasteboard symbols are unavailable")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	pool, _, _ := syscall_syscall(s.pushPool, 0, 0, 0)
	defer func() { _, _, _ = syscall_syscall(s.popPool, pool, 0, 0) }()

	board := s.send(s.typeNamed("NSPasteboard"), "pasteboardWithUniqueName", 0, 0, 0)
	if board == 0 {
		t.Fatal("could not create a unique pasteboard")
	}
	defer s.send(board, "releaseGlobally", 0, 0, 0)

	if paths, err := s.pasteboardFilePaths(board); err != nil || paths != nil {
		t.Fatalf("empty pasteboard = %q, %v; want no paths", paths, err)
	}

	dir := t.TempDir()
	want := []string{filepath.Join(dir, "screenshot.png"), filepath.Join(dir, "My Photos", "photo é.png")}
	// The files exist so the round trip can be judged by file identity. NSURL's fileSystemRepresentation, which darwin-platform.m returns unchanged (CLIPBOARD_FILES, upstream 0.99.1), is the file system's canonical spelling: decomposed Unicode ("é" as "e" plus U+0301), and a pasteboard file URL for a path under /private/tmp or /private/var may come back without the /private prefix. Neither is the spelling Go put in.
	for _, path := range want {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	urls := make([]uintptr, len(want))
	for i, path := range want {
		bytes := append([]byte(path), 0)
		text := s.send(s.typeNamed("NSString"), "alloc", 0, 0, 0)
		text = s.sendPointer(text, "initWithBytes:length:encoding:", unsafe.Pointer(&bytes[0]), uintptr(len(bytes)-1), 4) //nolint:gosec // G103: NSString copies this owned UTF-8 buffer synchronously; sendPointer keeps it alive and in place for the call.
		s.send(text, "autorelease", 0, 0, 0)
		urls[i] = s.send(s.typeNamed("NSURL"), "fileURLWithPath:", text, 0, 0)
	}
	array := s.sendPointer(s.typeNamed("NSArray"), "arrayWithObjects:count:", unsafe.Pointer(&urls[0]), uintptr(len(urls)), 0) //nolint:gosec // G103: NSArray copies the URL objects synchronously; sendPointer keeps the Go slice alive and in place for the call.
	s.send(board, "clearContents", 0, 0, 0)
	if s.send(board, "writeObjects:", array, 0, 0)&0xff == 0 {
		t.Fatal("could not write file URLs to the pasteboard")
	}

	got, err := s.pasteboardFilePaths(board)
	if err != nil || len(got) != len(want) {
		t.Fatalf("file paths = %q, %v; want the files %q", got, err, want)
	}
	for i := range want {
		wantInfo, err := os.Stat(want[i])
		if err != nil {
			t.Fatal(err)
		}
		gotInfo, err := os.Stat(got[i])
		if err != nil || !os.SameFile(gotInfo, wantInfo) {
			t.Fatalf("file path %d = %q (%v), want the file %q", i, got[i], err, want[i])
		}
	}

	// A cleared pasteboard holds no file URLs.
	s.send(board, "clearContents", 0, 0, 0)
	if paths, err := s.pasteboardFilePaths(board); err != nil || paths != nil {
		t.Fatalf("cleared pasteboard = %q, %v; want no paths", paths, err)
	}
}
