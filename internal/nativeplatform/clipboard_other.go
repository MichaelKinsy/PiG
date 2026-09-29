//go:build !linux && !windows && !darwin

package nativeplatform

func platformClipboard() *NativeClipboard { return nil }
