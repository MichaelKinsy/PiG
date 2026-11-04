package codingagent

import (
	"runtime"
	"testing"
)

// Pi 1be312b4 (#10488): home-to-"~" shortening anchors the home directory at a path segment in formatDisplayPath (interactive-mode.ts), the session selector's shortenPath and the tree selector's shortenPath. "/home/me2/x" was shown as "~2/x". paths.test.ts "shortenHomePath replaces only a leading home directory" has the inputs.
func TestHomeShorteningAnchorsAtAPathSegment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX path cases")
	}
	t.Setenv("HOME", "/home/me")
	t.Setenv("USERPROFILE", "/home/me")
	tree := &treeRowFormatter{home: "/home/me"}
	for _, tc := range []struct{ in, want string }{
		{"/home/me", "~"},
		{"/home/me/project", "~/project"},
		{"/home/me2/project", "/home/me2/project"},
		{"/home/Me/project", "/home/Me/project"},
		{"home/me/project", "home/me/project"},
	} {
		if got := formatDisplayPath(tc.in); got != tc.want {
			t.Errorf("formatDisplayPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
		if got := shortenSessionPath(tc.in); got != tc.want {
			t.Errorf("shortenSessionPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
		if got := tree.shortenPath(tc.in); got != tc.want {
			t.Errorf("treeRowFormatter.shortenPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
		if got := shortenPath(tc.in); got != tc.want {
			t.Errorf("shortenPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
