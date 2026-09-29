// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package harness

import "strings"

// Ports packages/agent/src/harness/system-prompt.ts.
// FormatSkillsForSystemPrompt lists model-visible skills in resource order and XML-escapes their fields.
func FormatSkillsForSystemPrompt(skills []Skill) string {
	lines := []string{
		"The following skills provide specialized instructions for specific tasks.",
		"Read the full skill file when the task matches its description.",
		"When a skill file references a relative path, resolve it against the skill directory (parent of SKILL.md / dirname of the path) and use that absolute path in tool commands.",
		"", "<available_skills>",
	}
	visible := false
	for _, skill := range skills {
		if skill.DisableModelInvocation {
			continue
		}
		visible = true
		lines = append(lines, "  <skill>", "    <name>"+skillXMLEscaper.Replace(skill.Name)+"</name>", "    <description>"+skillXMLEscaper.Replace(skill.Description)+"</description>", "    <location>"+skillXMLEscaper.Replace(skill.FilePath)+"</location>", "  </skill>")
	}
	if !visible {
		return ""
	}
	lines = append(lines, "</available_skills>")
	return strings.Join(lines, "\n")
}

var skillXMLEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
