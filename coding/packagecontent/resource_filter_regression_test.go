package packagecontent

import "testing"

// Pi 0.87.1 package-manager.ts:748-798 applies all excludes before exact force-includes and force-excludes, independently of pattern order.
func TestResourceEnabledUsesSharedPiPatternPhases(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, path string
		patterns   []string
		want       bool
	}{
		{"unset", "prompts/a.md", nil, true},
		{"empty", "prompts/a.md", []string{}, false},
		{"exclude", "extensions/foo.ts", []string{"!extensions/*.ts"}, false},
		{"globstar", "extensions/deep/foo.ts", []string{"!extensions/**/*.ts"}, false},
		{"basename", "themes/dark.json", []string{"*.json"}, true},
		{"force include precedes exclude", "extensions/foo.ts", []string{"+extensions/foo.ts", "!extensions/*.ts"}, true},
		{"force exclude precedes include", "extensions/foo.ts", []string{"-extensions/foo.ts", "+extensions/foo.ts"}, false},
		{"skill directory", "skills/foo/SKILL.md", []string{"!skills/foo"}, false},
		{"force paths are exact", "extensions/foo.ts", []string{"-extensions/*.ts"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ResourceEnabled(tc.path, tc.patterns); got != tc.want {
				t.Fatalf("ResourceEnabled(%q, %q) = %t, want %t", tc.path, tc.patterns, got, tc.want)
			}
		})
	}
}
