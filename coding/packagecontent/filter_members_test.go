package packagecontent

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// filterThrow is a pattern minimatch cannot compile: a POSIX class makes the RegExp use the u flag, which rejects the escaped ",".
const filterThrow = "[[:alpha:]]*,md"

// Pi's applyPackageFilter matches each member's absolute path with the Package root as the base (package-manager.ts:2280-2306), and matchesAnyPattern tries the path relative to that base, the base name and the whole path, and for a SKILL.md also its directory in those three forms (:661-685).
func TestMemberFiltersMatchPisCandidates(t *testing.T) {
	root := t.TempDir()
	prompt := filepath.Join(root, "prompts", "a.md")
	skill := filepath.Join(root, "skills", "my-skill", "SKILL.md")
	for _, tc := range []struct {
		name, member string
		patterns     []string
		want         bool
	}{
		{"relative path", prompt, []string{"prompts/a.md"}, true},
		{"base name", prompt, []string{"a.md"}, true},
		{"absolute path", prompt, []string{filepath.ToSlash(prompt)}, true},
		{"another member's absolute path", prompt, []string{filepath.ToSlash(filepath.Join(root, "prompts", "b.md"))}, false},
		{"relative member, absolute pattern", "prompts/a.md", []string{filepath.ToSlash(prompt)}, true},
		{"skill directory name", skill, []string{"my-skill"}, true},
		{"skill directory", skill, []string{"skills/my-skill"}, true},
		{"skill absolute directory", skill, []string{filepath.ToSlash(filepath.Dir(skill))}, true},
		{"excluded by absolute path", prompt, []string{"prompts/*.md", "!" + filepath.ToSlash(prompt)}, false},
		{"no filter", prompt, nil, true},
		{"empty filter", prompt, []string{}, false},
	} {
		got, err := MemberEnabled(root, tc.member, tc.patterns)
		if err != nil || got != tc.want {
			t.Errorf("%s: MemberEnabled(%q, %q) = %t, %v; want %t", tc.name, tc.member, tc.patterns, got, err, tc.want)
		}
	}
}

// minimatch's assertValidPattern throws a TypeError for a pattern longer than 65,536 UTF-16 units; Pi strips an exclude's "!" before minimatch sees it (applyPatterns, package-manager.ts:745-791). Recorded with Pi's npm minimatch 10.2.6.
func TestMinimatchFilterPatternLength(t *testing.T) {
	for _, tc := range []struct {
		name, pattern string
		fails         bool
	}{
		{"65,536 units", strings.Repeat("a", 65536), false},
		{"65,537 units", strings.Repeat("a", 65537), true},
		{"32,768 astral characters", strings.Repeat("\U0001F600", 32768), false},
		{"32,769 astral characters", strings.Repeat("\U0001F600", 32769), true},
	} {
		_, err := newMinimatchFilter(tc.pattern)
		if tc.fails && (err == nil || err.Error() != "pattern is too long") || !tc.fails && err != nil {
			t.Errorf("%s: error %v, want failure %t", tc.name, err, tc.fails)
		}
	}
	root := t.TempDir()
	if _, err := ApplyPatterns([]string{filepath.Join(root, "a")}, []string{"!" + strings.Repeat("a", 65536)}, root, Prompts); err != nil {
		t.Errorf("an exclude of 65,536 units after its \"!\": %v", err)
	}
}

// On Windows minimatch treats the "?" of a "//?/<drive>:" root as a literal, so the pattern does not match another UNC host; any other "?" stays magic. Recorded with Pi's npm minimatch 10.2.6 on Windows.
func TestMinimatchFilterWindowsUNCRoots(t *testing.T) {
	if !globIsWindows {
		t.Skip("minimatch keeps the ? of a //?/ root literal only on Windows")
	}
	for _, tc := range []struct {
		path, pattern string
		want          bool
	}{
		{"//?/C:/x/a", "//?/C:/x/*", true},
		{"//a/C:/x/a", "//?/C:/x/*", false},
		{"//a/C:/x/a", "//?/c:/x/*", false},
		{"//?/c:/x/a", "//?/C:/x/*", true},
		{"//?/x/a", "//?/x/*", true},
		{"//a/x/a", "//?/x/*", true},
		{"//a/b/c/a", "//?/b/c/*", true},
		{"C:/x/a", "//?/C:/x/*", true},
		{"//?/C:/x/a", "C:/x/*", true},
		{"//?/C:", "//?/C:", true},
		{"//a/C:", "//?/C:", false},
	} {
		m, err := newMinimatchFilter(tc.pattern)
		if err != nil {
			t.Fatal(err)
		}
		if got := m.match(tc.path); got != tc.want {
			t.Errorf("minimatch(%q, %q) = %t, want %t", tc.path, tc.pattern, got, tc.want)
		}
	}
}

// Pi's applyAutoloadDisabledPatterns calls matchesAnyPattern for each path and pattern (package-manager.ts:793-810), so a pattern compiles only when a path reaches it.
func TestAutoloadPatternsCompileWhenAPathReachesThem(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "prompts", "a.md")
	if _, err := ApplyAutoloadDisabledPatterns(nil, []string{"!" + filterThrow}, root, Prompts); err != nil {
		t.Fatalf("no path: %v", err)
	}
	if _, err := ApplyAutoloadDisabledPatterns([]string{path}, []string{"+prompts/a.md", "-" + filterThrow}, root, Prompts); err != nil {
		t.Fatalf("exact patterns compile nothing: %v", err)
	}
	if _, err := ApplyAutoloadDisabledPatterns([]string{path}, []string{"!" + filterThrow}, root, Prompts); err == nil {
		t.Fatal("a path that reaches the pattern: no error")
	}
}

// Pi's collectPackageResources applies a kind's filter right after collecting the kind, so a filter minimatch cannot compile ends a local Package's collection there; resolveLocalExtensionSource catches the throw silently (package-manager.ts:1372-1389). The kinds before it stay, and that kind and the later ones list no member and no missing member.
func TestConfiguredFilterThrowEndsTheInventory(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "package.json"), `{"name":"p","pi":{"skills":["skills"],"prompts":["prompts/a.md","prompts/missing.md"],"themes":["themes/dark.json","themes/missing.json"]}}`)
	writeTestFile(t, filepath.Join(root, "skills", "s", "SKILL.md"), "---\nname: s\ndescription: d\n---\nBody\n")
	writeTestFile(t, filepath.Join(root, "prompts", "a.md"), "A\n")
	writeTestFile(t, filepath.Join(root, "themes", "dark.json"), `{}`)
	for _, tc := range []struct {
		name    string
		filters map[Kind][]string
		prompts bool
	}{
		{"prompts filter throws", map[Kind][]string{Prompts: {filterThrow}}, false},
		{"skills filter throws", map[Kind][]string{Skills: {filterThrow}}, false},
		{"themes filter does not", map[Kind][]string{Themes: {"themes/*.json"}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resources, missing, _, err := ValidateConfiguredForStartup(root, tc.filters)
			if err != nil {
				t.Fatalf("startup validation reports %v; Pi reports nothing", err)
			}
			skills := len(tc.filters[Skills]) == 0
			if (len(resources.SkillDirs) == 1) != skills || (len(resources.PromptFiles) == 1) != tc.prompts || (len(resources.ThemeFiles) == 1) != tc.prompts {
				t.Fatalf("skills %q, prompts %q, themes %q; want skills %t, prompts and themes %t", resources.SkillDirs, resources.PromptFiles, resources.ThemeFiles, skills, tc.prompts)
			}
			var patterns []string
			for _, member := range missing {
				patterns = append(patterns, member.Pattern)
			}
			want := []string(nil)
			if tc.prompts {
				want = []string{"prompts/missing.md", "themes/missing.json"}
			}
			if !slices.Equal(patterns, want) {
				t.Fatalf("missing members %q, want %q", patterns, want)
			}
		})
	}
}

// A filter that only a missing member would reach throws nothing in Pi, because a missing member is no file it collects: inspection lists the member as disabled and goes on, and strict validation does not require it.
func TestConfiguredFilterOverAMissingMember(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "package.json"), `{"name":"p","pi":{"prompts":["prompts/missing.md"],"themes":["themes/missing.json"]}}`)
	filters := map[Kind][]string{Prompts: {filterThrow}}
	_, missing, err := InspectConfigured(root, filters)
	var got []string
	for _, member := range missing {
		got = append(got, fmt.Sprintf("%s:%t", member.Pattern, member.Enabled))
	}
	if err != nil || !slices.Equal(got, []string{"prompts/missing.md:false", "themes/missing.json:true"}) {
		t.Fatalf("missing %q, %v; want the prompt disabled and the theme enabled", got, err)
	}
	if _, err := ValidateConfigured(root, map[Kind][]string{Prompts: {filterThrow}, Themes: {}}); err != nil {
		t.Fatalf("strict validation = %v; want the disabled members not required", err)
	}
}
