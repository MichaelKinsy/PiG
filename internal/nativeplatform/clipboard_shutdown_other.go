//go:build !linux

package nativeplatform

// ShutdownClipboard has no private worker to join on platforms whose caller owns the complete native operation.
func ShutdownClipboard() {}
