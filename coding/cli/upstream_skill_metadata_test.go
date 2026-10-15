package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCLIPathSkillsUseTypedMetadata(t *testing.T) {
	root := t.TempDir()
	var paths []string
	for _, tc := range []struct{ name, fields string }{
		{"invalid", "description: true"},
		{"renamed", "name: true\ndescription: valid"},
		{"enabled", "description: valid\ndisable-model-invocation: \"true\""},
	} {
		dir := filepath.Join(root, tc.name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\n"+tc.fields+"\n---\nbody"), 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, dir)
	}
	skills, _, err := loadSkills(paths, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 2 || skills[0].Name != "renamed" || skills[1].Name != "enabled" || skills[1].DisableModelInvocation {
		t.Fatalf("typed skill metadata lost at CLI boundary: %#v", skills)
	}
}
