package packagecontent

import "testing"

// Pi package-manager.ts:739-786 applies include, exclude, exact force-include,
// then exact force-exclude groups, independent of their authored order.
func TestResourceEnabledUsesSharedPatternSemantics(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		patterns   []string
		want       bool
	}{
		{"omitted", "extensions/foo.ts", nil, true},
		{"empty", "extensions/foo.ts", []string{}, false},
		{"exclude extension", "extensions/bar.ts", []string{"!**/bar.ts"}, false},
		{"exclude theme basename", "themes/ugly.json", []string{"!ugly.json"}, false},
		{"exclude prompt basename", "prompts/explain.md", []string{"!explain.md"}, false},
		{"exclude skill directory", "skills/bad-skill/SKILL.md", []string{"!**/bad-skill"}, false},
		{"globstar nested", "extensions/nested/deep/foo.ts", []string{"**/foo.ts"}, true},
		{"globstar zero directories", "foo.ts", []string{"**/foo.ts"}, true},
		{"allow basename", "themes/dark.json", []string{"dark.json"}, true},
		{"allowlist nonmatch", "extensions/gamma.ts", []string{"**/alpha.ts", "**/beta.ts"}, false},
		{"exclude after includes", "extensions/beta.ts", []string{"!**/beta.ts", "**/alpha.ts", "**/beta.ts"}, false},
		{"force include after exclude", "extensions/beta.ts", []string{"+extensions/beta.ts", "!**/*.ts"}, true},
		{"force include is exact", "extensions/beta.ts", []string{"!**/*.ts", "+extensions/*.ts"}, false},
		{"force exclude is exact", "extensions/beta.ts", []string{"-extensions/*.ts"}, true},
		{"force exclude wins", "extensions/beta.ts", []string{"-extensions/beta.ts", "+extensions/beta.ts"}, false},
		{"force include dot prefix", "extensions/beta.ts", []string{"!**/*.ts", "+./extensions/beta.ts"}, true},
		{"force include skill directory", "skills/skill-a/SKILL.md", []string{"!**/*", "+skills/skill-a"}, true},
		{"force exclude skill directory", "skills/skill-a/SKILL.md", []string{"-skills/skill-a"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResourceEnabled(tc.path, tc.patterns); got != tc.want {
				t.Errorf("ResourceEnabled(%q, %q) = %t, want %t", tc.path, tc.patterns, got, tc.want)
			}
		})
	}
}

func BenchmarkResourceEnabledPackageFilters(b *testing.B) {
	paths := []string{"extensions/a.ts", "extensions/nested/b.ts", "prompts/review.md", "skills/review/SKILL.md", "themes/dark.json"}
	patterns := []string{"**/*", "!**/b.ts", "!**/review", "+skills/review", "-themes/dark.json"}
	for b.Loop() {
		for _, path := range paths {
			ResourceEnabled(path, patterns)
		}
	}
}
