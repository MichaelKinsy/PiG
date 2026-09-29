package nativeplatform

// Ports packages/tui/src/native-platform.ts

import (
	"context"
	"os"
	"runtime"
)

// NativeClipboard carries the native helper's callable capabilities. An unavailable read returns available=false; an available read with a nil value means an empty clipboard. SetText is absent on Linux, where command tools retain selection ownership. Go strings preserve lone UTF-16 surrogate units as WTF-8; OS byte APIs encode them as UTF-8 replacement characters.
// The additional optional functions belong to the same platform helper object, preserving its identity when accessed through either upstream getter.
type NativeClipboard struct {
	GetText                    func(context.Context) (value *string, available bool, err error)
	GetImage                   func(context.Context) (value []byte, available bool, err error)
	SetText                    func(context.Context, string) error
	EnableVirtualTerminalInput func() bool
	IsModifierPressed          func(string) bool
}

// GetNativeClipboard checks display presence without opening a connection. The helper object is cached; each read rechecks display availability through the OS.
func GetNativeClipboard() *NativeClipboard {
	return nativeClipboardFor(runtime.GOOS, runtime.GOARCH, os.Getenv, platformClipboard)
}

func nativeClipboardFor(platform, arch string, getenv func(string) string, load func() *NativeClipboard) *NativeClipboard {
	if arch != "amd64" && arch != "arm64" {
		return nil
	}
	if platform == "linux" && getenv("DISPLAY") == "" {
		return nil
	}
	if platform != "linux" && platform != "windows" && platform != "darwin" {
		return nil
	}
	return load()
}

// GetNativePlatformHelper returns the same helper used by clipboard access on Windows and macOS; Linux has only its lazy X11 clipboard helper.
func GetNativePlatformHelper() *NativeClipboard {
	if runtime.GOOS != "windows" && runtime.GOOS != "darwin" {
		return nil
	}
	return nativeClipboardFor(runtime.GOOS, runtime.GOARCH, os.Getenv, platformClipboard)
}
