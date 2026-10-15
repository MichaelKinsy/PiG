package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// Ports packages/coding-agent/test/package-manager.test.ts (Pi 1.2.0) "pattern filtering in top-level arrays" › "should expand ~ in
// exclusion and exact patterns" (#10765): a `!` glob and a `-` exact pattern that start with `~/` match the skills under the home
// directory that a `~/` skills entry loads.
func TestPackagePatternsExpandTildeInExclusionAndExactPatternsUpstream(t *testing.T) {
	f := newPackageResourceFixture(t)
	home := f.cwd // the fixture sets HOME to cwd, as the Pi test sets HOME to tempDir
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir reads USERPROFILE on Windows
	skillsDir := filepath.Join(home, "extra", "skills")
	for _, name := range []string{"keep", "glob-excluded", "exact-excluded"} {
		if err := os.MkdirAll(filepath.Join(skillsDir, name), 0o755); err != nil {
			t.Fatal(err)
		}
		writePackageResource(t, filepath.Join(skillsDir, name, "SKILL.md"), "---\nname: "+name+"\ndescription: "+name+"\n---\n")
	}

	if err := f.settings.SetSkillPaths([]string{"~/extra/skills", "!~/extra/skills/glob-*", "-~/extra/skills/exact-excluded"}); err != nil {
		t.Fatal(err)
	}

	items := f.items(t)
	requirePackageResource(t, items, filepath.Join(skillsDir, "keep", "SKILL.md"), tui.ResourceType("skills"), true)
	requirePackageResource(t, items, filepath.Join(skillsDir, "glob-excluded", "SKILL.md"), tui.ResourceType("skills"), false)
	requirePackageResource(t, items, filepath.Join(skillsDir, "exact-excluded", "SKILL.md"), tui.ResourceType("skills"), false)
}
