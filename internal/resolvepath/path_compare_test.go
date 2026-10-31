package resolvepath

import "testing"

// Ports packages/coding-agent/test/paths.test.ts "path comparison" (Pi 1be312b4, #10488). The Windows cases run on any host through OverridePlatform.
func TestPathComparisonTreatsDriveLettersAndComponentsCaseInsensitivelyOnWindows(t *testing.T) {
	t.Cleanup(OverridePlatform("windows"))
	if ComparisonKey(`c:\Users\me\.agents\skills`) != ComparisonKey(`C:\Users\Me\.agents\skills`) {
		t.Error("the keys of one directory in two spellings differ")
	}
	for _, tc := range []struct {
		name      string
		got, want bool
	}{
		{"equal across casing and separators", PathsEqual(`c:\Users\me`, "C:/Users/Me/"), true},
		{"other drive", PathsEqual(`C:\Users\me`, `D:\Users\me`), false},
		{"within across casing", IsPathWithin(`C:\Users\Me`, `c:\users\me\project`), true},
		{"sibling with a longer name", IsPathWithin(`C:\Users\Me`, `C:\Users\Me2`), false},
		{"drive root", IsPathWithin(`C:\`, `c:\Users`), true},
		{"UNC share", IsPathWithin(`\\server\share`, `\\SERVER\Share\folder`), true},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %v, want %v", tc.name, tc.got, tc.want)
		}
	}
}

func TestPathComparisonKeepsCasingOnCaseSensitivePlatforms(t *testing.T) {
	t.Cleanup(OverridePlatform("linux"))
	if PathsEqual("/home/me", "/home/Me") {
		t.Error("/home/me equals /home/Me")
	}
	if IsPathWithin("/home/me", "/home/Me/project") {
		t.Error("/home/Me/project is within /home/me")
	}
}

func TestIsPathWithinMatchesWholePathSegments(t *testing.T) {
	t.Cleanup(OverridePlatform("linux"))
	for _, tc := range []struct {
		parent, child string
		want          bool
	}{
		{"/home/me", "/home/me", true},
		{"/home/me", "/home/me/project", true},
		{"/home/me/", "/home/me/project", true},
		{"/home/me", "/home/me2", false},
		{"/", "/home", true},
	} {
		if got := IsPathWithin(tc.parent, tc.child); got != tc.want {
			t.Errorf("IsPathWithin(%q, %q) = %v, want %v", tc.parent, tc.child, got, tc.want)
		}
	}
}
