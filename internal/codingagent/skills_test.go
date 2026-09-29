package codingagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func TestLoadSkillsFromPath_RootDirectory(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"alpha", "beta"} {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+name+"\n---\nbody"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	skills, err := LoadSkillsFromPath(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 2 {
		t.Fatalf("len = %d, want 2", len(skills))
	}
	if skills[0].Name != "alpha" || skills[1].Name != "beta" {
		t.Fatalf("names = %q, %q", skills[0].Name, skills[1].Name)
	}
}

func TestLoadSkillsFromPath_RecursesAndLoadsRootMarkdown(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "group", "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "SKILL.md"), []byte("---\nname: nested\ndescription: nested\n---\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "flat.md"), []byte("---\nname: flat\ndescription: flat\n---\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}

	skills, err := LoadSkillsFromPath(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 2 || skills[0].Name != "flat" || skills[1].Name != "nested" {
		t.Fatalf("skills = %#v", skills)
	}
}

func TestLoadSkillsFromPath_SkipsUnreadableOrMalformedSibling(t *testing.T) {
	root := t.TempDir()
	bad := filepath.Join(root, "bad")
	good := filepath.Join(root, "good")
	if err := os.MkdirAll(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(good, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad, "SKILL.md"), []byte("---\ndescription: [unterminated\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(good, "SKILL.md"), []byte("---\nname: good\ndescription: good\n---\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}

	skills, err := LoadSkillsFromPath(root)
	if err == nil {
		t.Fatal("malformed sibling produced no warning error")
	}
	if len(skills) != 1 || skills[0].Name != "good" {
		t.Fatalf("skills = %#v", skills)
	}
}

func TestLoadSkillsFromPath_DedupsSymlinkedSkill(t *testing.T) {
	root := t.TempDir()
	targetDir := filepath.Join(root, "alpha")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "SKILL.md"), []byte("---\nname: alpha\n---\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}
	aliasDir := filepath.Join(root, "alias-alpha")
	testenv.RequireDirectoryLink(t, targetDir, aliasDir)

	skills, err := LoadSkillsFromPath(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 {
		t.Fatalf("len = %d, want 1", len(skills))
	}
	if skills[0].Name != "alpha" {
		t.Fatalf("name = %q, want alpha", skills[0].Name)
	}
}

func TestLoadSkillPathParsesInvocationAndValidationMetadata(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "Bad--Name-")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: Bad--Name-\ndescription: valid description\ndisable-model-invocation: true\n---\nbody"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	skill, err := LoadSkillPath(skillDir)
	if err != nil {
		t.Fatal(err)
	}
	if !skill.DisableModelInvocation {
		t.Fatal("disable-model-invocation was ignored")
	}
	got := SkillDiagnostics(skill)
	if len(got) != 3 {
		t.Fatalf("diagnostics = %v", got)
	}
}

func TestLoadSkillPath_AllowsNameDifferentFromDirectory(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "directory-name")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: custom-name\ndescription: ok\n---\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}
	skill, err := LoadSkillPath(skillDir)
	if err != nil {
		t.Fatal(err)
	}
	if skill.Name != "custom-name" {
		t.Fatalf("skill.Name = %q, want custom-name", skill.Name)
	}
}

// Upstream loadSkillFromFile (core/skills.ts) skips, with no diagnostic, a
// Markdown file other than SKILL.md that has no description or whose
// frontmatter does not parse, whether it is named directly or found at the
// root of a skills directory; loadSkills warns about a file path that is not
// Markdown.
func TestLoadSkillsFromPath_SkipsMarkdownThatIsNotASkill(t *testing.T) {
	root := t.TempDir()
	for name, content := range map[string]string{
		"SUITE.md":             "# Suite\n\nDocumentation, not a skill.\n",
		"broken.md":            "---\nname: [unclosed\n---\nbody",
		"listed.md":            "---\nname: listed\ndescription: listed\n---\nbody",
		"notes.txt":            "---\ndescription: text\n---\n",
		"inner/SKILL.md":       "---\nname: inner\ndescription: inner\n---\nbody",
		"undescribed/SKILL.md": "---\nname: undescribed\n---\nbody",
	} {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	skills, err := LoadSkillsFromPath(filepath.Join(root, "SUITE.md"))
	if err != nil || len(skills) != 0 {
		t.Fatalf("SUITE.md = %#v, %v; want skipped", skills, err)
	}
	skills, err = LoadSkillsFromPath(filepath.Join(root, "broken.md"))
	if err != nil || len(skills) != 0 {
		t.Fatalf("broken.md = %#v, %v; want skipped", skills, err)
	}
	if _, err := LoadSkillsFromPath(filepath.Join(root, "notes.txt")); err == nil || !strings.Contains(err.Error(), "not a markdown file") {
		t.Fatalf("notes.txt error = %v", err)
	}
	// A declared SKILL.md is always a skill: a missing description stays a
	// diagnostic for the caller.
	skills, err = LoadSkillsFromPath(filepath.Join(root, "undescribed", "SKILL.md"))
	if err != nil || len(skills) != 1 {
		t.Fatalf("undescribed = %#v, %v", skills, err)
	}

	skills, err = LoadSkillsFromPath(root)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, skill := range skills {
		names = append(names, skill.Name)
	}
	if strings.Join(names, ",") != "inner,listed,undescribed" {
		t.Fatalf("names = %v", names)
	}
}
