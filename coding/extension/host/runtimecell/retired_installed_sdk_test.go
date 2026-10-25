package runtimecell

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pigsdklock"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// For a binary whose SDKs are installed, a replace into another installation's SDK (a package store keeps an earlier installation until it
// is collected) is retired wherever that installation lives, because the copy carries the version marker only staging and installation
// write. The current installed SDK and an unmarked source checkout stay authoritative. A binary that stages its SDKs keeps the rule it
// had before installed SDKs existed: only a copy in its own config root's staging area is retired, so a replace into another config root
// or installation still resolves.
func TestRetiredStagedSDKRecognizesAnotherInstallation(t *testing.T) {
	store := t.TempDir()
	current := filepath.Join(store, "aaaa-pig-0.5.4", "share", "pig", "sdk", "sdk-1111111111111111")
	previous := filepath.Join(store, "bbbb-pig-0.5.3", "share", "pig", "sdk", "sdk-2222222222222222")
	checkout := filepath.Join(t.TempDir(), "PiG", "extensions", "sdk")
	for dir, marker := range map[string]string{current: "1111111111111111", previous: "2222222222222222", checkout: ""} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if marker != "" {
			if err := os.WriteFile(filepath.Join(dir, pigsdklock.MarkerFile), []byte(marker+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	link := filepath.Join(t.TempDir(), "sdk-link")
	testenv.RequireDirectoryLink(t, previous, link)
	for _, tc := range []struct {
		dir       string
		installed bool
		want      bool
	}{
		{previous, true, true},
		{link, true, true},
		{current, true, false},
		{checkout, true, false},
		{previous, false, false},
		{link, false, false},
		{current, false, false},
		{checkout, false, false},
	} {
		root := ""
		if tc.installed {
			root = filepath.Dir(current)
		}
		restore := pigsdklock.SetInstalledSDKRootForTest(root)
		if got := RetiredStagedSDK(tc.dir, current); got != tc.want {
			t.Errorf("installed=%v: RetiredStagedSDK(%s) = %v, want %v", tc.installed, tc.dir, got, tc.want)
		}
		restore()
	}
}
