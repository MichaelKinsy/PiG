package packagecontent

import "testing"

// package-manager.ts matchesAnyPattern and node-glob decide a resource filter with minimatch (Pi 1.1.0 package-manager.ts:674-684). The expected values
// come from running minimatch 10.2.6 on the same pairs; the matcher itself is compared with minimatch over generated pairs in internal/minimatch.
func TestMatchGlobUsesMinimatchSyntax(t *testing.T) {
	for _, tc := range []struct {
		name, pattern string
		want          bool
	}{
		{"extensions/a.ts", "extensions/{a,b}.ts", true},
		{"extensions/c.ts", "extensions/{a,b}.ts", false},
		{"skills/x/SKILL.md", "skills/+(x|y)/**", true},
		{"skills/z/SKILL.md", "!skills/z/**", false},
		{"a/b.ts", "a/@(b|c).ts", true},
		{"a/d.ts", "a/!(b|c).ts", true},
		{"a/.hid.ts", "a/*.ts", false},
		{"a/b/c/d.ts", "**/d.ts", true},
		{"a/b.ts", "a/[[:alpha:]].ts", true},
	} {
		if got := matchGlob(tc.name, tc.pattern); got != tc.want {
			t.Errorf("matchGlob(%q, %q) = %v, minimatch %v", tc.name, tc.pattern, got, tc.want)
		}
	}
}

func TestMatchesAnyPatternAcceptsBraceAndExtglobFilters(t *testing.T) {
	base := "/pkg"
	for _, tc := range []struct {
		file     string
		patterns []string
		want     bool
	}{
		{"/pkg/extensions/a.ts", []string{"extensions/{a,b}.ts"}, true},
		{"/pkg/extensions/c.ts", []string{"extensions/{a,b}.ts"}, false},
		{"/pkg/extensions/c.ts", []string{"!(a|b).ts"}, true},
	} {
		if got := matchesAnyPattern(tc.file, tc.patterns, base, Extensions); got != tc.want {
			t.Errorf("matchesAnyPattern(%q, %v) = %v, want %v", tc.file, tc.patterns, got, tc.want)
		}
	}
}
