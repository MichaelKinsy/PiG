package ignorerules

import (
	"fmt"
	"path/filepath"
	"testing"
)

// Pi's ignore matcher retains its rules for the discovery walk (skills.ts:187-188).
// Candidate checks must not rebuild regex programs. The empty-rule call measures
// only the same platform-specific relative-path work, independently of the matcher.
func TestIgnoreRulesCandidateAllocationBudget(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector drops sync.Pool objects at random, so regexp reallocates its pooled matchers; the budget is enforced without -race")
	}
	root := filepath.Join(t.TempDir(), "skills")
	candidate := filepath.Join(root, "ordinary", "SKILL.md")
	rules := AppendPatterns(nil, []string{"build/**", "*.bak", "!keep/**", "generated/", "**/scratch?"})
	baseline := testing.AllocsPerRun(10, func() { Ignored(candidate, false, root, nil) })
	allocs := testing.AllocsPerRun(10, func() {
		if Ignored(candidate, false, root, rules) {
			t.Fatal("ordinary skill was ignored")
		}
	})
	if allocs > baseline {
		t.Fatalf("candidate allocations = %.0f, relative-path baseline = %.0f; rules must be compiled before candidate checks", allocs, baseline)
	}
}

func TestIgnoreRulesOrderAndWalkOwnership(t *testing.T) {
	root := t.TempDir()
	rules := AppendPatterns(nil, []string{"*.bak", "generated/**", "!generated/keep.md", "cache/"})
	for _, tc := range []struct {
		path      string
		dir, want bool
	}{
		{"ordinary/SKILL.md", false, false},
		{"ordinary/file.bak", false, true},
		{"generated/drop.md", false, true},
		{"generated/keep.md", false, false},
		{"cache", true, true},
		{"cache/file.md", false, true},
	} {
		if got := Ignored(filepath.Join(root, tc.path), tc.dir, root, rules); got != tc.want {
			t.Errorf("%s: ignored = %v, want %v", tc.path, got, tc.want)
		}
	}
	later := AppendPatterns(rules, []string{"generated/keep.md"})
	path := filepath.Join(root, "generated/keep.md")
	if Ignored(path, false, root, rules) || !Ignored(path, false, root, later) {
		t.Fatal("appending a later rule changed the earlier rule set or lost precedence")
	}
	if Ignored(path, false, root, AppendPatterns(nil, []string{"other/**"})) {
		t.Fatal("a fresh walk retained prior rules")
	}
}

func BenchmarkIgnoreRulesResourceWalk(b *testing.B) {
	root := filepath.Join(b.TempDir(), "skills")
	patterns := make([]string, 64)
	for i := range patterns {
		patterns[i] = fmt.Sprintf("generated-%d/**", i)
	}
	rules := AppendPatterns(nil, patterns)
	paths := make([]string, 256)
	for i := range paths {
		paths[i] = filepath.Join(root, fmt.Sprintf("skill-%d", i), "SKILL.md")
	}
	b.ReportAllocs()
	for b.Loop() {
		for _, path := range paths {
			if Ignored(path, false, root, rules) {
				b.Fatal("ordinary skill was ignored")
			}
		}
	}
}

// pi: the `ignore` package that package-manager.ts and skills.ts use. Every row is the value `ignore().add(pattern).ignores(path)` returns for a file, or for the path with a trailing slash for a directory.
func TestIgnoreRulesFollowTheIgnorePackage(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct {
		pattern, path string
		dir, want     bool
	}{
		{"build/", "build", false, false}, // a trailing slash selects directories only
		{"build/", "build", true, true},
		{"build/", "src/build", true, true},
		{"build/", "BUILD", true, true}, // `ignore()` matches case-insensitively
		{"Build", "build", false, true},
		{"*.LOG", "x.log", false, true},
		{"/dist", "a/dist", true, false}, // a leading slash anchors the pattern
		{"a/b", "x/a/b", false, false},   // a slash inside a pattern anchors it
		{"!keep", "keep", false, false},
		{"docs/*/", "docs/a", true, true},
		{"docs/*/", "docs/a", false, false},
	} {
		got := Ignored(filepath.Join(root, tc.path), tc.dir, root, AppendPatterns(nil, []string{tc.pattern}))
		if got != tc.want {
			t.Errorf("pattern %q path %q dir=%v: ignored = %v, Pi %v", tc.pattern, tc.path, tc.dir, got, tc.want)
		}
	}
}
