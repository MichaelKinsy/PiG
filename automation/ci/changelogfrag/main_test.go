// SPDX-License-Identifier: MIT

package main

import (
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFragments(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func problemsOf(t *testing.T, files map[string]string) string {
	t.Helper()
	_, problems := load(writeFragments(t, files))
	var msg []string
	for _, p := range problems {
		msg = append(msg, p.Error())
	}
	return strings.Join(msg, "\n")
}

func TestFragmentRules(t *testing.T) {
	const good = "### Fixed\n\n- The loop exits.\n  A wrapped line.\n"
	for name, tc := range map[string]struct {
		files map[string]string
		want  string // empty: no problem
	}{
		"valid":              {map[string]string{"loop-0123abc.md": good}, ""},
		"name without sha":   {map[string]string{"loop.md": good}, "is named <lane>-<sha>.md"},
		"uppercase lane":     {map[string]string{"Loop-0123abc.md": good}, "is named <lane>-<sha>.md"},
		"short sha":          {map[string]string{"loop-012.md": good}, "is named <lane>-<sha>.md"},
		"no heading":         {map[string]string{"loop-0123abc.md": "- The loop exits.\n"}, "before the first"},
		"unknown section":    {map[string]string{"loop-0123abc.md": "### Misc\n\n- x\n"}, "is not one of"},
		"paragraph":          {map[string]string{"loop-0123abc.md": "### Fixed\n\nFixed the loop.\n"}, "neither a bullet"},
		"no bullet":          {map[string]string{"loop-0123abc.md": "### Fixed\n"}, "no bullet"},
		"not markdown":       {map[string]string{"loop-0123abc.txt": good}, "only .md"},
		"legacy name exempt": {map[string]string{}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			saved := legacyList
			defer func() { legacyList = saved }()
			legacyList = "legacy-fragment.md\n"
			files := map[string]string{"legacy-fragment.md": "Fixed text.\n"}
			maps.Copy(files, tc.files)
			got := problemsOf(t, files)
			if tc.want == "" && got != "" || tc.want != "" && !strings.Contains(got, tc.want) {
				t.Fatalf("problems = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRenderGroupsBySectionInFragmentNameOrder(t *testing.T) {
	saved := legacyList
	defer func() { legacyList = saved }()
	legacyList = ""
	dir := writeFragments(t, map[string]string{
		"b-0123abc.md": "### Fixed\n\n- Fix B.\n\n### Added\n\n- Add B.\n",
		"a-0123abc.md": "### Fixed\n\n- Fix A.\n  wrapped\n",
	})
	fragments, problems := load(dir)
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	want := "## [Unreleased]\n\n### Added\n\n- Add B.\n\n### Fixed\n\n- Fix A.\n  wrapped\n- Fix B.\n"
	if got := render(fragments); got != want {
		t.Fatalf("render:\n%q\nwant:\n%q", got, want)
	}
}

// A legacy fragment without sections is printed under "### Unsorted", not dropped, and a legacy entry whose file is
// gone fails the check.
func TestLegacyFragmentsAreNeverDroppedAndTheListOnlyShrinks(t *testing.T) {
	saved := legacyList
	defer func() { legacyList = saved }()
	legacyList = "old-bullets.md\nold-sections.md\n"
	dir := writeFragments(t, map[string]string{
		"old-bullets.md":  "- Old bullet without a section.\n",
		"old-sections.md": "### Fixed\n\n- Old fix.\n",
		"a-0123abc.md":    "### Fixed\n\n- Fix A.\n",
	})
	fragments, problems := load(dir)
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	want := "## [Unreleased]\n\n### Fixed\n\n- Fix A.\n- Old fix.\n\n### Unsorted\n\n<!-- changelog.d/old-bullets.md -->\n- Old bullet without a section.\n"
	if got := render(fragments); got != want {
		t.Fatalf("render:\n%q\nwant:\n%q", got, want)
	}
	if err := os.Remove(filepath.Join(dir, "old-bullets.md")); err != nil {
		t.Fatal(err)
	}
	if _, problems := load(dir); len(problems) != 1 || !strings.Contains(problems[0].Error(), "legacy.txt lists old-bullets.md") {
		t.Fatalf("problems = %v, want the stale legacy entry", problems)
	}
}

// The fold appends each fragment bullet to its section of the release, adds a missing section in section order, keeps
// other headings and releases unchanged, and deletes the folded fragments and their legacy lines; a second fold then
// takes only the fragments written since.
func TestFoldAppendsToTheReleaseAndDeletesTheFragments(t *testing.T) {
	saved := legacyList
	defer func() { legacyList = saved }()
	legacyList = "old-sections.md\n"
	root := t.TempDir()
	dir := filepath.Join(root, "changelog.d")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{
		"old-sections.md": "### Fixed\n\n- Old fix.\n",
		"b-0123abc.md":    "### Removed\n\n- Remove B.\n\n### Upgrading\n\n- Upgrade B.\n\n### Added\n\n- Add B.\n  wrapped\n",
		"a-0123abc.md":    "### Fixed\n\n- Fix A. <!-- fix: 0123abcdef -->\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	changelog := filepath.Join(root, "CHANGELOG.md")
	const before = "# Changelog\n\n## [Unreleased]\n\n## [1.0.0] - 2026-10-15\n\n### Upgrading from 0.9.x\n\n- Note.\n\n### Added\n\n- Add X.\n\n### Fixed\n\n- Fix X.\n\n## [0.9.0] - 2026-01-01\n\n### Fixed\n\n- Old.\n"
	legacyPath := filepath.Join(root, "legacy.txt")
	for path, text := range map[string]string{changelog: before, legacyPath: "kept.md\nold-sections.md\n"} {
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fold := func(fragments []fragment) {
		t.Helper()
		if err := foldInto(dir, changelog, legacyPath, "1.0.0", fragments); err != nil {
			t.Fatal(err)
		}
	}
	fragments, problems := load(dir)
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	fold(fragments)
	want := "# Changelog\n\n## [Unreleased]\n\n## [1.0.0] - 2026-10-15\n\n### Upgrading from 0.9.x\n\n- Note.\n- Upgrade B.\n\n### Added\n\n- Add X.\n- Add B.\n  wrapped\n\n### Removed\n\n- Remove B.\n\n### Fixed\n\n- Fix X.\n- Fix A. <!-- fix: 0123abcdef -->\n- Old fix.\n\n## [0.9.0] - 2026-01-01\n\n### Fixed\n\n- Old.\n"
	assertFile(t, changelog, want)
	assertFile(t, legacyPath, "kept.md\n")
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Fatalf("changelog.d still holds %d fragments", len(left))
	}

	legacyList = ""
	// Git does not keep the emptied directory; a checkout without it has no fragments and passes the check.
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if fragments, problems := load(dir); len(fragments) != 0 || len(problems) != 0 {
		t.Fatalf("load of a missing changelog.d = %v, %v", fragments, problems)
	}
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "c-0123abc.md"), []byte("### Fixed\n\n- Fix C.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fragments, problems = load(dir)
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	fold(fragments)
	assertFile(t, changelog, strings.Replace(want, "- Old fix.\n", "- Old fix.\n- Fix C.\n", 1))
}

func TestFoldRefusesUnsortedFragmentsAndAMissingRelease(t *testing.T) {
	const changelog = "## [Unreleased]\n\n## [1.0.0] - 2026-10-15\n\n### Fixed\n\n- Fix X.\n"
	if _, err := fold(changelog, []fragment{{name: "old.md", unsorted: "- Old."}}, "1.0.0"); err == nil || !strings.Contains(err.Error(), "old.md") {
		t.Fatalf("fold of an unsorted fragment: err = %v", err)
	}
	if _, err := fold(changelog, []fragment{{name: "a-0123abc.md", items: map[string][]string{"Fixed": {"Fix A."}}}}, "1.1.0"); err == nil {
		t.Fatal("fold into a missing release succeeded")
	}
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("%s:\n%q\nwant:\n%q", path, got, want)
	}
}
