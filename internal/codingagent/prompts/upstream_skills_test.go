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
	// formatSkillsForPrompt (skills.ts) emits the exact listing for both file-read tools, led by the blank line that system-prompt.ts trims.
	t.Run("emits the exact listing for the read and bash tools", func(t *testing.T) {
		const tail = "\nWhen a skill file references a relative path, resolve it against the skill directory (parent of SKILL.md / dirname of the path) and use that absolute path in tool commands.\n\n<available_skills>\n  <skill>\n    <name>test-skill</name>\n    <description>A test skill.</description>\n    <location>/path/to/skill/SKILL.md</location>\n  </skill>\n</available_skills>"
		const head = "\n\nThe following skills provide specialized instructions for specific tasks.\n"
		if got, want := formatSkills([]Skill{skill}, "read"), head+"Use the read tool to load a skill's file when the task matches its description."+tail; got != want {
			t.Fatalf("read listing =\n%q\nwant\n%q", got, want)
		}
		if got, want := formatSkills([]Skill{skill}, "bash"), head+"Use bash to load a skill's file when the task matches its description."+tail; got != want {
			t.Fatalf("bash listing =\n%q\nwant\n%q", got, want)
		}
	})
	// skills.ts escapeXml replaces all five XML specials in every field, and system-prompt.ts trims the listing into the skills section, which buildSystemPromptSections wraps in a <skills> tag. The expected listing is Pi 1.0.0's formatSkillsForPrompt(skills, "bash").trim() under Node 24.
	t.Run("escapes every field and trims the skills section", func(t *testing.T) {
		const want = "<skills>\nThe following skills provide specialized instructions for specific tasks.\nUse bash to load a skill's file when the task matches its description.\nWhen a skill file references a relative path, resolve it against the skill directory (parent of SKILL.md / dirname of the path) and use that absolute path in tool commands.\n\n<available_skills>\n  <skill>\n    <name>it&apos;s</name>\n    <description>&lt;&amp;&gt;&quot;&apos;</description>\n    <location>/p/&apos;&quot;&amp;&lt;&gt;/SKILL.md</location>\n  </skill>\n</available_skills>\n</skills>"
		options := Options{Cwd: "/work", Tools: []string{"bash"}, Skills: []Skill{{Name: "it's", Description: `<&>"'`, Path: `/p/'"&<>/SKILL.md`}}}
		for _, section := range BuildSystemPromptSections(options) {
			if section.Name == "skills" {
				if section.Value == nil {
					t.Fatal("skills section has no value")
				}
				if *section.Value != want {
					t.Fatalf("skills section =\n%q\nwant\n%q", *section.Value, want)
				}
				return
			}
		}
		t.Fatal("missing skills section")
	})
}
