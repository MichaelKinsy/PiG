package harness_test

import (
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func TestUpstreamHarnessSkills(t *testing.T) {
	// .upstream/v0.87.1/packages/agent/test/harness/skills.test.ts:10
	t.Run("loads SKILL.md files through the execution environment", func(t *testing.T) {
		root, e := resourceTestEnv(t, map[string]string{".agents/skills/example/SKILL.md": "---\nname: example\ndescription: Example skill\ndisable-model-invocation: true\n---\nUse this skill.\n"})
		got, diagnostics := harness.LoadSkills(t.Context(), e, []string{".agents/skills"})
		want := []harness.Skill{{Name: "example", Description: "Example skill", Content: "Use this skill.", FilePath: filepath.Join(root, ".agents/skills/example/SKILL.md"), DisableModelInvocation: true}}
		if len(diagnostics) != 0 || !reflect.DeepEqual(got, want) {
			t.Fatalf("skills=%#v diagnostics=%#v; want %#v", got, diagnostics, want)
		}
	})
	// .upstream/v0.87.1/packages/agent/test/harness/skills.test.ts:40
	t.Run("loads skills through symlinked directories", func(t *testing.T) {
		root, e := resourceTestEnv(t, map[string]string{"actual/example/SKILL.md": "---\nname: example\ndescription: Example skill\n---\nUse this skill."})
		testenv.RequireDirectoryLink(t, filepath.Join(root, "actual"), filepath.Join(root, "skills-link"))
		got, _ := harness.LoadSkills(t.Context(), e, []string{"skills-link"})
		if len(got) != 1 || got[0].Name != "example" || got[0].FilePath != filepath.Join(root, "skills-link/example/SKILL.md") {
			t.Fatalf("skills=%#v", got)
		}
	})
	// .upstream/v0.87.1/packages/agent/test/harness/skills.test.ts:57
	t.Run("preserves source info for sourced skills", func(t *testing.T) {
		root, e := resourceTestEnv(t, map[string]string{"user/example/SKILL.md": "---\nname: example\ndescription: Example skill\n---\nUse this skill."})
		source := resourceSource{Type: "user"}
		got, diagnostics, err := harness.LoadSourcedSkills(t.Context(), e, []harness.SourcedPath[resourceSource]{{Path: "user", Source: source}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		want := []harness.SourcedSkill[resourceSource]{{Skill: harness.Skill{Name: "example", Description: "Example skill", Content: "Use this skill.", FilePath: filepath.Join(root, "user/example/SKILL.md")}, Source: source}}
		if len(diagnostics) != 0 || !reflect.DeepEqual(got, want) {
			t.Fatalf("skills=%#v diagnostics=%#v; want %#v", got, diagnostics, want)
		}
	})
	// .upstream/v0.87.1/packages/agent/test/harness/skills.test.ts:89
	t.Run("attaches source info to diagnostics", func(t *testing.T) {
		root, e := resourceTestEnv(t, map[string]string{"user/broken/SKILL.md": "---\nname: broken\n---\nMissing description."})
		source := resourceSource{Type: "user"}
		got, diagnostics, err := harness.LoadSourcedSkills(t.Context(), e, []harness.SourcedPath[resourceSource]{{Path: "user", Source: source}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		want := []harness.SourcedSkillDiagnostic[resourceSource]{{SkillDiagnostic: harness.SkillDiagnostic{Type: "warning", Code: "invalid_metadata", Message: "description is required", Path: filepath.Join(root, "user/broken/SKILL.md")}, Source: source}}
		if len(got) != 0 || !reflect.DeepEqual(diagnostics, want) {
			t.Fatalf("skills=%#v diagnostics=%#v; want %#v", got, diagnostics, want)
		}
	})
	// .upstream/v0.87.1/packages/agent/test/harness/skills.test.ts:114
	t.Run("loads direct markdown children only from the root directory", func(t *testing.T) {
		_, e := resourceTestEnv(t, map[string]string{"skills/root.md": "---\ndescription: Root skill\n---\nRoot content", "skills/nested/ignored.md": "---\ndescription: Ignored\n---\nIgnored content"})
		got, _ := harness.LoadSkills(t.Context(), e, []string{"skills"})
		if len(got) != 1 || got[0].Name != "skills" || got[0].Content != "Root content" {
			t.Fatalf("skills=%#v", got)
		}
	})
	// .upstream/v0.87.1/packages/agent/test/harness/skills.test.ts:131
	t.Run("ignores root markdown docs that do not declare skills", func(t *testing.T) {
		_, e := resourceTestEnv(t, map[string]string{"skills/README.md": "# Shared skills\n\nDocumentation.", "skills/AGENTS.md": "# Agent notes\n\nDocumentation.", "skills/CLAUDE.md": "---\ndescription: [invalid\n---\n\nDocumentation.", "skills/root.md": "---\ndescription: Root skill\n---\nRoot content", "skills/nested-skill/SKILL.md": "---\nname: nested-skill\ndescription: Nested skill\n---\nNested content"})
		got, diagnostics := harness.LoadSkills(t.Context(), e, []string{"skills"})
		names := make([]string, 0, len(got))
		for _, skill := range got {
			names = append(names, skill.Name)
		}
		slices.Sort(names)
		if len(diagnostics) != 0 || !slices.Equal(names, []string{"nested-skill", "skills"}) {
			t.Fatalf("skills=%#v diagnostics=%#v", got, diagnostics)
		}
	})
}

func TestHarnessSkillsRootPrecedenceAndIgnoreFiles(t *testing.T) {
	_, e := resourceTestEnv(t, map[string]string{
		"skills/.gitignore":           "skip/\n",
		"skills/skip/SKILL.md":        "---\ndescription: Skipped\n---\nignored",
		"skills/outer/SKILL.md":       "---\ndescription: Outer\n---\nouter",
		"skills/outer/inner/SKILL.md": "---\ndescription: Inner\n---\ninner",
	})
	got, diagnostics := harness.LoadSkills(t.Context(), e, []string{"skills"})
	if len(diagnostics) != 0 || len(got) != 1 || got[0].Name != "outer" {
		t.Fatalf("skills=%#v diagnostics=%#v", got, diagnostics)
	}
}
