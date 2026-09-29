//go:build !linux

package subprocess_test

import "errors"

// linkNativeClipboardAssets is never reached off Linux: native-clipboard-linux prepares its C fixtures only there.
func linkNativeClipboardAssets(string, string) error {
	return errors.New("native clipboard fixtures are Linux only")
}
