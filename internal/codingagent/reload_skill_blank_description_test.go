package codingagent

import (
	"os"
	"path/filepath"
	"testing"
)

// skills.ts loadSkillFromFile drops a skill whose description.trim() is empty (JavaScript whitespace: U+FEFF and U+00A0 are blank, U+0085 is not) and keeps
// the others. The reload filter must apply the same trim: a description made only of U+0085 survives, one made only of U+FEFF does not.
func TestReloadSkillsFiltersBlankDescriptionsByJSTrim(t *testing.T) {
	m, _ := newExtensionDialogProbe(t)
	root := t.TempDir()
	for name, description := range map[string]string{
		"nel":  `"\u0085"`,
		"bom":  `"\ufeff"`,
		"nbsp": `"\u00a0 "`,
		"text": "text",
	} {
		path := filepath.Join(root, name, "SKILL.md")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("---\nname: "+name+"\ndescription: "+description+"\n---\nbody"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m.opts.SkillPaths = []string{root}
	m.reloadSkillsFromPaths()
	var got []string
	for _, skill := range m.opts.Skills {
		got = append(got, skill.Name)
	}
	if len(got) != 2 || got[0] != "nel" || got[1] != "text" {
		t.Fatalf("reloaded skills = %q, want [nel text]", got)
	}
}
