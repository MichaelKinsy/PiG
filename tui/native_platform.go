package tui

// Ports packages/tui/src/native-platform.ts

import "github.com/MichaelKinsy/PiG/internal/nativeplatform"

// NativeClipboard is the platform helper's clipboard and optional modifier capabilities. Read methods are blocking Go equivalents of the upstream promises; invoke them off the input/render loop. available=false represents undefined, while a nil value with available=true represents null. GetFilePaths exists only on macOS, where Finder file copies are file URLs; elsewhere it is nil, upstream's undefined.
type NativeClipboard = nativeplatform.NativeClipboard

func GetNativeClipboard() *NativeClipboard      { return nativeplatform.GetNativeClipboard() }
func GetNativePlatformHelper() *NativeClipboard { return nativeplatform.GetNativePlatformHelper() }
