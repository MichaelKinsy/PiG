package tui

// pi: packages/coding-agent/src/core/tools/render-utils.ts

import (
	"runtime"
	"testing"
)

// TestRenderToolPathHonorsPiGeneratedExpectations pins renderToolPath, shortenPath and linkPath (render-utils.ts:10-23, 102-112). The
// expectations were produced by the Pi 1.1.0 render-utils.js with HOME=/home/tester: shortenPath anchors home at a path segment since Pi 1be312b4 (#10488; the 1.1.0 expectation was ~x for /home/testerx), and linkPath resolves the raw path against cwd (`~` expanded) before
// pathToFileURL percent-encodes it.
func TestRenderToolPathHonorsPiGeneratedExpectations(t *testing.T) {
	th := ActiveTheme()
	c := func(color, s string) string { return color + s + SGRFgReset }
	t.Setenv("HOME", "/home/tester")
	t.Setenv("USERPROFILE", "/home/tester")
	previous := GetCapabilities()
	t.Cleanup(func() { SetCapabilities(previous) })
	SetCapabilities(TerminalCapabilities{})

	cases := []struct {
		name          string
		raw           string
		ok            bool
		emptyFallback string
		want          string
	}{
		{"str() rejected the argument", "", false, "", c(th.Error, "[invalid arg]")},
		{"empty path without a fallback", "", true, "", c(th.ToolOutput, "...")},
		{"empty path uses the fallback", "", true, ".", c(th.Accent, ".")},
		{"home prefix", "/home/tester/a.go", true, "", c(th.Accent, "~/a.go")},
		{"home is a path segment, not a string prefix", "/home/testerx/a.go", true, "", c(th.Accent, "/home/testerx/a.go")},
		{"home with a trailing slash", "/home/tester/", true, "", c(th.Accent, "~")},
		{"outside home", "/srv/a.go", true, "", c(th.Accent, "/srv/a.go")},
	}
	for _, tc := range cases {
		if got := renderToolPathArg(tc.raw, tc.ok, "/work", tc.emptyFallback); got != tc.want {
			t.Errorf("%s: renderToolPathArg(%q) = %q, want %q", tc.name, tc.raw, got, tc.want)
		}
	}

	if runtime.GOOS == "windows" {
		return // pathToFileURL of a POSIX cwd differs on Windows
	}
	SetCapabilities(TerminalCapabilities{Hyperlinks: true})
	for _, tc := range []struct{ raw, want string }{
		{"src/a b#1.go", "\x1b]8;;file:///work/src/a%20b%231.go\x1b\\S\x1b]8;;\x1b\\"},
		{"~/x.go", "\x1b]8;;file:///home/tester/x.go\x1b\\S\x1b]8;;\x1b\\"},
	} {
		if got := linkPath("S", tc.raw, "/work"); got != tc.want {
			t.Errorf("linkPath(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}
