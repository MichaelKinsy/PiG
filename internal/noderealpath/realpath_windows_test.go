//go:build windows

package noderealpath

import "testing"

func TestVolumeGUIDPathClassification(t *testing.T) {
	for path, want := range map[string]bool{
		`\\?\Volume{1234}\`:           true,
		`\\?\volume{1234}\child`:      true,
		`C:\Volume{1234}`:             false,
		`\\?\C:\ordinary`:             false,
		`\\server\share\Volume{1234}`: false,
	} {
		if got := isVolumeGUIDPath(path); got != want {
			t.Errorf("isVolumeGUIDPath(%q)=%v; want %v", path, got, want)
		}
	}
}
