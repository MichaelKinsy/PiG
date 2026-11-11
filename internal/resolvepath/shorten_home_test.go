package resolvepath

import "testing"

// pi: packages/coding-agent/src/utils/paths.ts

// Ports test/paths.test.ts "shortenHomePath replaces only a leading home directory" (Pi 1be312b4, #10488).
func TestShortenHomePathReplacesOnlyALeadingHomeDirectory(t *testing.T) {
	defer OverridePlatform("linux")()
	for _, tc := range []struct{ path, home, want string }{
		{"/home/me", "/home/me", "~"},
		{"/home/me/project", "/home/me", "~/project"},
		{"/home/me2/project", "/home/me", "/home/me2/project"},
		{"/home/Me/project", "/home/me", "/home/Me/project"},
		{"/etc/passwd", "/", "~/etc/passwd"},
		{"home/me/project", "/home/me", "home/me/project"},
		{"/home/me/project", "", "/home/me/project"},
	} {
		if got := ShortenHomePath(tc.path, tc.home); got != tc.want {
			t.Errorf("ShortenHomePath(%q, %q) = %q, want %q", tc.path, tc.home, got, tc.want)
		}
	}
}

// Ports test/paths.test.ts "shortenHomePath ignores casing and keeps separators on Windows" with a simulated Windows platform (Pi 1be312b4).
func TestShortenHomePathIgnoresCaseOnWindows(t *testing.T) {
	defer OverridePlatform("windows")()
	for _, tc := range []struct{ path, home, want string }{
		{`c:\users\me\project`, `C:\Users\Me`, `~\project`},
		{`\\server\share\folder`, `\\server\share`, `~\folder`},
		{`C:\Users\Me`, `c:\users\me`, "~"},
		{`C:\Users\Me2\x`, `C:\Users\Me`, `C:\Users\Me2\x`},
	} {
		if got := ShortenHomePath(tc.path, tc.home); got != tc.want {
			t.Errorf("ShortenHomePath(%q, %q) = %q, want %q", tc.path, tc.home, got, tc.want)
		}
	}
}
