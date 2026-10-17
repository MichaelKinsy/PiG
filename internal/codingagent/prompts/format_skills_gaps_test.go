package prompts

import (
	"strings"
	"testing"
)

// skills.ts formatSkillsForPrompt: the load instruction names the read tool by default, bash for a bash-only tool set and nothing tool-specific when the read is indirect.
// Pi: packages/coding-agent/src/core/skills.ts formatSkillsForPrompt.
func TestFormatSkillsLoadInstructionFollowsTheReadTool(t *testing.T) {
	skills := []Skill{{Name: "s", Description: "d", Path: "/p/SKILL.md"}}
	for tool, want := range map[string]string{
		"read":     "Use the read tool to load a skill's file when the task matches its description.",
		"":         "Use the read tool to load a skill's file when the task matches its description.",
		"bash":     "Use bash to load a skill's file when the task matches its description.",
		"indirect": "Load a skill's file when the task matches its description.",
	} {
		lines := strings.Split(formatSkills(skills, tool), "\n")
		if len(lines) < 4 || lines[3] != want {
			t.Errorf("readTool %q: instruction line %q, want %q", tool, lines[3:4], want)
		}
	}
}
