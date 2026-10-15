package packagecontent

import (
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// Pi 1.2.0 matchesAnyPattern and normalizeExactPattern (package-manager.ts:678, 695-697) pass every pattern through normalizePath (Pi #10765):
// a leading `~` or `~/` expands to the home directory, a file:// URL becomes its path, and an exact pattern loses a leading `./` first.
func TestPatternsAreNormalizedLikePaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir reads USERPROFILE on Windows
	keep := filepath.Join(home, "claude-backup", "skills", "keep")
	drop := filepath.Join(home, "claude-backup", "skills", "pdf")
	writeTestFile(t, filepath.Join(keep, "SKILL.md"), "# keep\n")
	writeTestFile(t, filepath.Join(drop, "SKILL.md"), "# pdf\n")
	baseDir := t.TempDir()
	paths := Collect([]string{filepath.Join(home, "claude-backup", "skills")}, Skills)
	if len(paths) != 2 {
		t.Fatalf("collected %v", paths)
	}
	dropURL := "file://" + filepath.ToSlash(drop)
	if runtime.GOOS == "windows" {
		dropURL = "file:///" + filepath.ToSlash(drop)
	}
	for _, tc := range []struct {
		name     string
		patterns []string
		want     []string
	}{
		{"exclude", []string{"!~/claude-backup/skills/pdf"}, []string{keep}},
		{"force exclude", []string{"-~/claude-backup/skills/pdf"}, []string{keep}},
		{"include wildcard", []string{"~/claude-backup/skills/k*"}, []string{keep}},
		{"force include", []string{"~/claude-backup/skills/nothing", "+~/claude-backup/skills/pdf"}, []string{drop}},
		{"a tilde in the middle stays literal", []string{"!claude-backup/~/pdf"}, []string{drop, keep}},
		{"an exact pattern drops ./ before it expands ~", []string{"-./~/claude-backup/skills/pdf"}, []string{keep}},
		{"a file URL pattern is its path", []string{"!" + dropURL}, []string{keep}},
		{"a file URL exact pattern is its path", []string{"-" + dropURL}, []string{keep}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ApplyPatterns(paths, tc.patterns, baseDir, Skills)
			if err != nil {
				t.Fatalf("ApplyPatterns(%v): %v", tc.patterns, err)
			}
			slices.Sort(got)
			slices.Sort(tc.want)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("ApplyPatterns(%v) = %v, want %v", tc.patterns, got, tc.want)
			}
		})
	}
	if runtime.GOOS != "windows" {
		// normalizePath throws Node's ERR_INVALID_FILE_URL_HOST for a file URL with a host, so Pi's resolve fails on such a pattern.
		for _, pattern := range []string{"!file://host/x", "-file://host/x"} {
			if _, err := ApplyPatterns(paths, []string{pattern}, baseDir, Skills); err == nil || !strings.Contains(err.Error(), `File URL host must be "localhost" or empty`) {
				t.Errorf("ApplyPatterns(%q) error = %v, want the invalid file URL host error", pattern, err)
			}
		}
	}
}
