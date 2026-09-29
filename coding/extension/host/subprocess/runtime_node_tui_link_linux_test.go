//go:build linux

package subprocess_test

import "os"

// linkNativeClipboardAssets points the original C fixtures' ../../native include at the shipped implementation. Only the Linux-only native-clipboard-linux run calls it.
func linkNativeClipboardAssets(target, link string) error { return os.Symlink(target, link) }
