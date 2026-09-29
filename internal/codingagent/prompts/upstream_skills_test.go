package prompts

import (
	"strings"
	"testing"
)

func TestUpstreamCoreFormatSkillsForPrompt(t *testing.T) {
	skill := Skill{Name: "test-skill", Description: "A test skill.", Path: "/path/to/skill/SKILL.md"}
	// .upstream/v0.87.1/packages/coding-agent/test/skills.test.ts:225
	t.Run("should return empty string for no skills", func(t *testing.T) {
		if got := formatSkills(nil, "read"); got != "" {
			t.Fatalf("prompt=%q", got)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/skills.test.ts:230
	t.Run("should format skills as XML", func(t *testing.T) {
		got := formatSkills([]Skill{skill}, "read")
		for _, want := range []string{"<available_skills>", "</available_skills>", "<skill>", "<name>test-skill</name>", "<description>A test skill.</description>", "<location>/path/to/skill/SKILL.md</location>"} {
			if !strings.Contains(got, want) {
				t.Fatalf("prompt lacks %q: %q", want, got)
			}
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/skills.test.ts:250
	t.Run("should include intro text before XML", func(t *testing.T) {
		got := formatSkills([]Skill{skill}, "read")
		before, _, ok := strings.Cut(got, "<available_skills>")
		if !ok {
			t.Fatal(got)
		}
		intro := before
		for _, want := range []string{"The following skills provide specialized instructions", "Use the read tool to load a skill's file"} {
			if !strings.Contains(intro, want) {
				t.Fatalf("intro lacks %q: %q", want, intro)
			}
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/skills.test.ts:268
	t.Run("should escape XML special characters", func(t *testing.T) {
		s := skill
		s.Description = `A skill with <special> & "characters".`
		got := formatSkills([]Skill{s}, "read")
		for _, want := range []string{"&lt;special&gt;", "&amp;", "&quot;characters&quot;"} {
			if !strings.Contains(got, want) {
				t.Fatalf("prompt lacks %q: %q", want, got)
			}
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/skills.test.ts:285
	t.Run("should format multiple skills", func(t *testing.T) {
		got := formatSkills([]Skill{{Name: "skill-one", Description: "First skill.", Path: "/path/one/SKILL.md"}, {Name: "skill-two", Description: "Second skill.", Path: "/path/two/SKILL.md"}}, "read")
		if !strings.Contains(got, "<name>skill-one</name>") || !strings.Contains(got, "<name>skill-two</name>") || strings.Count(got, "<skill>") != 2 {
			t.Fatalf("prompt=%q", got)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/skills.test.ts:308
	t.Run("should exclude skills with disableModelInvocation from prompt", func(t *testing.T) {
		got := formatSkills([]Skill{{Name: "visible-skill", Description: "A visible skill.", Path: "/path/visible/SKILL.md"}, {Name: "hidden-skill", Description: "A hidden skill.", Path: "/path/hidden/SKILL.md", DisableModelInvocation: true}}, "read")
		if !strings.Contains(got, "<name>visible-skill</name>") || strings.Contains(got, "<name>hidden-skill</name>") || strings.Count(got, "<skill>") != 1 {
			t.Fatalf("prompt=%q", got)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/skills.test.ts:332
	t.Run("should return empty string when all skills have disableModelInvocation", func(t *testing.T) {
		got := formatSkills([]Skill{{Name: "hidden-skill", Description: "A hidden skill.", Path: "/path/hidden/SKILL.md", DisableModelInvocation: true}}, "read")
		if got != "" {
			t.Fatalf("prompt=%q", got)
		}
	})
}
