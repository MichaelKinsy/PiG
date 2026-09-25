package tui

// skill_invocation.go: skill invocation display component.
//
// Ports upstream skill-invocation-message.ts (55 LOC).
// Renders a skill block with collapsed/expanded state.

import "strings"

// ParsedSkillBlock holds the parsed skill invocation data.
// Mirrors upstream's ParsedSkillBlock.
type ParsedSkillBlock struct {
	Name    string
	Content string
}

// SkillInvocationMessageComponent renders a skill invocation message.
type SkillInvocationMessageComponent struct {
	invalidatable
	skillBlock ParsedSkillBlock
	expanded   bool
}

// NewSkillInvocationMessage creates a skill invocation component.
func NewSkillInvocationMessage(block ParsedSkillBlock) *SkillInvocationMessageComponent {
	return &SkillInvocationMessageComponent{skillBlock: block}
}

// SetExpanded toggles expanded/collapsed rendering.
func (s *SkillInvocationMessageComponent) SetExpanded(expanded bool) {
	s.expanded = expanded
	s.Invalidate()
}

// Render produces the skill invocation lines.
// Mirrors upstream SkillInvocationMessageComponent which extends Box(paddingX=1, paddingY=1).
// In pig's line renderer, paddingY manifests as empty rows above and below content.
func (s *SkillInvocationMessageComponent) Render(width int) []string {
	t := ActiveTheme()
	customMsgBgOpen := t.CustomMessageBg

	if s.expanded {
		var lines []string
		// paddingY top
		lines = append(lines, paintBgWith(customMsgBgOpen, "", width))
		label := t.CustomMessageLabel + "\x1b[1m[skill]\x1b[22m\x1b[0m"
		lines = append(lines, paintBgWith(customMsgBgOpen, " "+label, width))
		// Header: bold skill name.
		header := "\x1b[1m" + s.skillBlock.Name + "\x1b[22m"
		lines = append(lines, paintBgWith(customMsgBgOpen, " "+t.CustomMessageText+header+"\x1b[0m", width))
		lines = append(lines, paintBgWith(customMsgBgOpen, "", width))
		// Content lines.
		for line := range strings.SplitSeq(s.skillBlock.Content, "\n") {
			lines = append(lines, paintBgWith(customMsgBgOpen, " "+t.CustomMessageText+line+"\x1b[0m", width))
		}
		// paddingY bottom
		lines = append(lines, paintBgWith(customMsgBgOpen, "", width))
		return lines
	}

	// Collapsed: single line: [skill] name (hint to expand).
	// Upstream Box paddingY=1 adds 1 blank row above and below.
	line := t.CustomMessageLabel + "\x1b[1m[skill]\x1b[22m\x1b[0m " +
		t.CustomMessageText + s.skillBlock.Name + "\x1b[0m" +
		t.Dim + " (ctrl+o to expand)" + "\x1b[0m"
	return []string{
		paintBgWith(customMsgBgOpen, "", width), // paddingY top
		paintBgWith(customMsgBgOpen, " "+line, width),
		paintBgWith(customMsgBgOpen, "", width), // paddingY bottom
	}
}
