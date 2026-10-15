package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

var blankSkillDescriptions = []struct{ name, description string }{
	{"nel", `"\u0085"`}, {"bom", `"\ufeff"`}, {"nbsp", `"\u00a0 "`}, {"text", "text"},
}

func writeBlankDescriptionSkills(t *testing.T, root string) {
	t.Helper()
	for _, skill := range blankSkillDescriptions {
		writeResourceLoaderFixture(t, filepath.Join(root, skill.name, "SKILL.md"), "---\nname: "+skill.name+"\ndescription: "+skill.description+"\n---\nbody")
	}
}

// skills.ts drops a skill whose description.trim() is empty (JavaScript whitespace: U+FEFF and U+00A0 are blank, U+0085 is not). Skills an extension adds after
// session_start in print, JSON and RPC mode go through the same filter (the Session's cliResourceLoader.ExtendResources).
func TestExtensionDiscoveredSkillsAreFilteredByJSTrim(t *testing.T) {
	root := t.TempDir()
	cwd := filepath.Join(root, "project")
	skillRoot := filepath.Join(root, "skills")
	writeBlankDescriptionSkills(t, skillRoot)
	loader := newCLIResourceLoader(cwd, filepath.Join(root, "agent"))
	if err := loader.ExtendResources(coding.ResourceExtensionPaths{SkillPaths: []coding.ExtensionResourcePath{{Path: skillRoot}}}); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, skill := range loader.GetSkills().Skills {
		got = append(got, skill.Name)
	}
	if len(got) != 2 || got[0] != "nel" || got[1] != "text" {
		t.Fatalf("skills = %q, want [nel text]", got)
	}
}

// --skill NAME resolves NAME under the agent skills directory and keeps the skill unless its description is blank by JavaScript trim.
func TestNamedSkillsAreFilteredByJSTrim(t *testing.T) {
	skillsDir := codingagent.DefaultSkillsDir()
	writeBlankDescriptionSkills(t, skillsDir)
	t.Cleanup(func() {
		for _, skill := range blankSkillDescriptions {
			_ = os.RemoveAll(filepath.Join(skillsDir, skill.name))
		}
	})
	var got []string
	for _, skill := range blankSkillDescriptions {
		defs, _, err := loadSkills([]string{skill.name}, false)
		if err != nil {
			t.Fatal(err)
		}
		if len(defs) == 1 {
			got = append(got, defs[0].Name)
		}
	}
	if len(got) != 2 || got[0] != "nel" || got[1] != "text" {
		t.Fatalf("named skills kept = %q, want [nel text]", got)
	}
}
