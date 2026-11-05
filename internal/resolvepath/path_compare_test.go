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

// HasComparisonKey is ComparisonKey(path) == key for a resolved path, without the allocations of PathsEqual.
func TestHasComparisonKeyAgreesWithComparisonKeyOnWindows(t *testing.T) {
	t.Cleanup(OverridePlatform("windows"))
	paths := []string{
		`C:\`, `c:\`, `C:\Users`, `C:\Users\Me`, `c:\users\me`, `C:\Users\Me\project`, `C:\Users\Me2`, `D:\Users\Me`,
		`\\server\share\`, `\\SERVER\Share\`, `\\server\share\folder`,
		"C:/Users/Me",
		`C:\Users\Ünïcode`, `c:\users\ünïcode`, `C:\Users\İstanbul`, `C:\Users\K`, `C:\Users\k`, "C:\\Users\\bad\xffbyte", "c:\\users\\bad\ufffdbyte",
	}
	for _, path := range paths {
		for _, other := range paths {
			key := ComparisonKey(other)
			if got, want := HasComparisonKey(path, key), ComparisonKey(path) == key; got != want {
				t.Errorf("HasComparisonKey(%q, ComparisonKey(%q)) = %v, want %v", path, other, got, want)
			}
		}
	}
	key := ComparisonKey(`C:\Users\Me`)
	if allocs := testing.AllocsPerRun(100, func() { HasComparisonKey(`C:\USERS\me`, key) }); allocs != 0 {
		t.Errorf("HasComparisonKey allocates %v objects", allocs)
	}
}

func TestHasComparisonKeyComparesBytesOnCaseSensitivePlatforms(t *testing.T) {
	t.Cleanup(OverridePlatform("linux"))
	key := ComparisonKey("/home/me")
	if !HasComparisonKey("/home/me", key) || HasComparisonKey("/home/Me", key) {
		t.Error("HasComparisonKey folded case on a case-sensitive platform")
	}
}
