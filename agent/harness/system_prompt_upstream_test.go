// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package harness

import (
	"strings"
	"testing"
)

func TestFormatSkillsForSystemPromptUpstream(t *testing.T) {
	visible := Skill{Name: "visible", Description: "Use <this> & that", Content: "visible content", FilePath: "/skills/visible/SKILL.md"}
	second := Skill{Name: "second", Description: "Second skill", Content: "second content", FilePath: "/skills/second/SKILL.md"}
	disabled := Skill{Name: "hidden", Description: "Hidden", Content: "hidden content", FilePath: "/skills/hidden/SKILL.md", DisableModelInvocation: true}
	// .upstream/v0.87.1/packages/agent/test/harness/system-prompt.test.ts:27
	t.Run("formats visible skills in order and skips model-disabled skills", func(t *testing.T) {
		want := `The following skills provide specialized instructions for specific tasks.
Read the full skill file when the task matches its description.
When a skill file references a relative path, resolve it against the skill directory (parent of SKILL.md / dirname of the path) and use that absolute path in tool commands.

<available_skills>
  <skill>
    <name>visible</name>
    <description>Use &lt;this&gt; &amp; that</description>
    <location>/skills/visible/SKILL.md</location>
  </skill>
  <skill>
    <name>second</name>
    <description>Second skill</description>
    <location>/skills/second/SKILL.md</location>
  </skill>
</available_skills>`
		if got := FormatSkillsForSystemPrompt([]Skill{visible, disabled, second}); got != want {
			t.Fatalf("got:\n%s\nwant:\n%s", got, want)
		}
	})
	// .upstream/v0.87.1/packages/agent/test/harness/system-prompt.test.ts:48
	t.Run("returns an empty string when no skills are model-visible", func(t *testing.T) {
		if got := FormatSkillsForSystemPrompt([]Skill{disabled}); got != "" {
			t.Fatal(got)
		}
		if got := FormatSkillsForSystemPrompt(nil); got != "" {
			t.Fatal(got)
		}
	})
	// .upstream/v0.87.1/packages/agent/test/harness/system-prompt.test.ts:52
	t.Run("escapes XML in all model-visible skill fields", func(t *testing.T) {
		got := FormatSkillsForSystemPrompt([]Skill{{Name: "a&b", Description: `Quote "double" and 'single'`, Content: "content", FilePath: `/skills/<bad>&"quote"/SKILL.md`}})
		want := "<name>a&amp;b</name>\n    <description>Quote &quot;double&quot; and &apos;single&apos;</description>\n    <location>/skills/&lt;bad&gt;&amp;&quot;quote&quot;/SKILL.md</location>"
		if !strings.Contains(got, want) {
			t.Fatal(got)
		}
	})
}
