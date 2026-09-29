package main

import "testing"

// The inventory accounts for Pig's exported Go surface, which a compiler patch
// release does not change. Recording the patch made interface-go-drift depend on
// which toolchain generated the file: CI builds in a pinned image while
// developers run whatever their version manager installed, so a committed file
// could satisfy one environment and never the other.
func TestLanguageVersionIgnoresThePatchRelease(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"go1.26.1", "1.26"},
		{"go1.26.4", "1.26"},
		{"go1.26", "1.26"},
		{"go1.27.0-rc1", "1.27"},
		{"devel", "devel"},
	} {
		if got := languageVersion(tc.in); got != tc.want {
			t.Errorf("languageVersion(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Two toolchains differing only by patch must produce the same value, which is
// the property the drift gate depends on.
func TestPatchReleasesAgree(t *testing.T) {
	if languageVersion("go1.26.1") != languageVersion("go1.26.4") {
		t.Error("a patch bump still changes the recorded version, so CI and local can disagree")
	}
}
