//go:build !linux

package nativeplatform

// Ports packages/tui/src/native-platform.ts (the Linux worker's shutdown has no counterpart elsewhere).

// ShutdownClipboard has no private worker to join on platforms whose caller owns the complete native operation.
func ShutdownClipboard() {}
