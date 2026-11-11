package runtime

import "testing"

// Ports "abbreviates macOS, Linux, root and Termux homes..." and "leaves other directories ... unchanged"
// (test/p2/path-label.test.ts, Pi main ea153dd28 moved shortPath to src/runtime/location.ts).
func TestShortPathAbbreviatesHomesAndLeavesOtherDirectoriesUnchanged(t *testing.T) {
	for _, home := range []string{"/Users/armin", "/home/armin", "/root", "/data/data/com.termux/files/home"} {
		for _, each := range [][2]string{
			{home, "~"},
			{home + "/", "~/"},
			{home + "/Development/pi-mono", "~/Development/pi-mono"},
		} {
			if got := ShortPath(each[0]); got != each[1] {
				t.Errorf("ShortPath(%q) = %q, want %q", each[0], got, each[1])
			}
		}
	}
	for _, path := range []string{
		"/",
		"/Users",
		"/home",
		"/rooted/project",
		"/data/data/com.termux/files/homeless/project",
		"/work/Users/armin/project",
		"/work/home/armin/project",
		"/work/project",
		"~/project",
		"project",
		"",
		`C:\Users\armin\project`,
	} {
		if got := ShortPath(path); got != path {
			t.Errorf("ShortPath(%q) = %q, want it unchanged", path, got)
		}
	}
}
