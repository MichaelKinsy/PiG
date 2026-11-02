//go:build !linux && !windows && !darwin

package nativeplatform

// Ports packages/tui/src/native-platform.ts (getNativePlatformHelper returns undefined on platforms with no helper).

func platformClipboard() *NativeClipboard { return nil }
