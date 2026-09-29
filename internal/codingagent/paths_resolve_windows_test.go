//go:build windows

package codingagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Expected values are Pi 0.87.1 resolvePath and getCwdRelativePath (utils/paths.ts) on Windows with Node 24: isAbsolute accepts a rooted path without a drive, and path.resolve puts it on the process's drive, not the base directory's. path.resolve keeps a trailing dot in the last segment.
func TestResolvePathRootedAndTrailingDotMatchNodeOnWindows(t *testing.T) {
	processCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	drive := filepath.VolumeName(processCwd)
	// The base must be on another drive, or the old base-drive rule gives the same answer.
	other := "D:"
	if strings.EqualFold(drive, other) {
		other = "C:"
	}
	for _, tc := range []struct{ input, base, want string }{
		{`\tools\x`, other + `\base`, drive + `\tools\x`},
		{`/tools/x`, other + `\base`, drive + `\tools\x`},
		{`\tools\x.`, other + `\base`, drive + `\tools\x.`},
		{other + `\tools\x.`, drive + `\base`, other + `\tools\x.`},
		{`tools\x.`, other + `\base`, other + `\base\tools\x.`},
	} {
		got, err := ResolvePath(tc.input, tc.base)
		if err != nil || got != tc.want {
			t.Errorf("ResolvePath(%q, %q) = %q, %v; want Node's %q", tc.input, tc.base, got, err, tc.want)
		}
	}
	if got := GetCwdRelativePath(`\tools\x.`, drive+`\tools`); got != `x.` {
		t.Errorf("GetCwdRelativePath keeps the trailing dot: got %q", got)
	}
}
