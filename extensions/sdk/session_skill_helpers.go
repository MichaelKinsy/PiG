package sdk

import (
	"slices"
	"strings"
)

// Pi library helpers over session entries and skills: CURRENT_SESSION_VERSION (core/session-manager.ts:41), VIRTUAL_MODEL_STATE_ENTRY (core/virtual-models.ts:32),
// getLatestCompactionEntry (core/session-manager.ts:372), parseSkillBlock (core/agent-session.ts:177) and formatSkillsForPrompt (core/skills.ts:358).

const (
	// CurrentSessionVersion is Pi's CURRENT_SESSION_VERSION: the version a session file's header carries.
	CurrentSessionVersion = 3
	// VirtualModelStateEntry is Pi's VIRTUAL_MODEL_STATE_ENTRY: the custom entry type that persists the virtual model state.
	VirtualModelStateEntry = "pi.virtual-model-state"
)

// GetLatestCompactionEntry is Pi's getLatestCompactionEntry: the last "compaction" entry of the session entries, or nil.
func GetLatestCompactionEntry(entries []map[string]any) map[string]any {
	for _, entrie := range slices.Backward(entries) {
		if kind, _ := entrie["type"].(string); kind == "compaction" {
			return entrie
		}
	}
	return nil
}

// ParsedSkillBlock is Pi's ParsedSkillBlock.
type ParsedSkillBlock struct {
	Name     string
	Location string
	Content  string
	// UserMessage is the trimmed text after the block, nil where Pi's is undefined (no text, or only white space).
	UserMessage *string
}

// ParseSkillBlock is Pi's parseSkillBlock: it reads `<skill name="N" location="L">\n CONTENT \n</skill>` optionally followed by `\n\n` and the user's message,
// where the block must span the whole text, or returns nil. The content ends at the first "\n</skill>" that leaves either nothing or "\n\n" and at least one more
// character, as Pi's lazy pattern does.
func ParseSkillBlock(text string) *ParsedSkillBlock {
	rest, ok := strings.CutPrefix(text, `<skill name="`)
	if !ok {
		return nil
	}
	name, rest, ok := strings.Cut(rest, `"`)
	if !ok || name == "" {
		return nil
	}
	if rest, ok = strings.CutPrefix(rest, ` location="`); !ok {
		return nil
	}
	location, rest, ok := strings.Cut(rest, `"`)
	if !ok || location == "" {
		return nil
	}
	if rest, ok = strings.CutPrefix(rest, ">\n"); !ok {
		return nil
	}
	const closing = "\n</skill>"
	for offset := 0; ; {
		at := strings.Index(rest[offset:], closing)
		if at < 0 {
			return nil
		}
		at += offset
		after := rest[at+len(closing):]
		if after == "" {
			return &ParsedSkillBlock{Name: name, Location: location, Content: rest[:at]}
		}
		if user, ok := strings.CutPrefix(after, "\n\n"); ok && user != "" {
			block := &ParsedSkillBlock{Name: name, Location: location, Content: rest[:at]}
			if trimmed := jsTrim(user); trimmed != "" {
				block.UserMessage = &trimmed
			}
			return block
		}
		offset = at + 1
	}
}

// jsTrim is JavaScript's String.prototype.trim: it removes the ECMAScript white space and line terminators, which include U+FEFF and not U+0085.
func jsTrim(text string) string {
	return strings.TrimFunc(text, func(r rune) bool {
		switch r {
		case '\t', '\n', '\v', '\f', '\r', ' ', 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
			return true
		}
		return r >= 0x2000 && r <= 0x200A
	})
}

// FormatSkillsForPrompt is Pi's formatSkillsForPrompt: the XML block that lists the skills a model may load, without those that disable model invocation. fileReadTool
// names the tool that loads a skill's file: "read" (Pi's default), "bash", or any other value for a reader reachable only through another tool ("indirect").
func FormatSkillsForPrompt(skills []SystemPromptSkill, fileReadTool string) string {
	var visible []SystemPromptSkill
	for _, skill := range skills {
		if !skill.DisableModelInvocation {
			visible = append(visible, skill)
		}
	}
	if len(visible) == 0 {
		return ""
	}
	load := "Load a skill's file when the task matches its description."
	switch fileReadTool {
	case "read":
		load = "Use the read tool to load a skill's file when the task matches its description."
	case "bash":
		load = "Use bash to load a skill's file when the task matches its description."
	}
	lines := []string{
		"\n\nThe following skills provide specialized instructions for specific tasks.",
		load,
		"When a skill file references a relative path, resolve it against the skill directory (parent of SKILL.md / dirname of the path) and use that absolute path in tool commands.",
		"",
		"<available_skills>",
	}
	for _, skill := range visible {
		lines = append(lines, "  <skill>", "    <name>"+escapeXML(skill.Name)+"</name>", "    <description>"+escapeXML(skill.Description)+"</description>",
			"    <location>"+escapeXML(skill.FilePath)+"</location>", "  </skill>")
	}
	lines = append(lines, "</available_skills>")
	return strings.Join(lines, "\n")
}

var xmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")

func escapeXML(text string) string { return xmlEscaper.Replace(text) }
