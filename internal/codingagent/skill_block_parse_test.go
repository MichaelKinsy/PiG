package codingagent

import "testing"

// Upstream agent-session.ts parseSkillBlock: the regex captures name, location and body; the trailing user message
// is String.prototype.trim()ed and becomes undefined when empty.
func TestParseSkillBlockMatchesUpstream(t *testing.T) {
	const block = "<skill name=\"deploy\" location=\"/skills/deploy/SKILL.md\">\nReferences are relative to /skills/deploy.\n\nbody\n</skill>"
	for _, tc := range []struct {
		name, text string
		want       *ParsedSkillBlockFromText
	}{
		{"block only", block, &ParsedSkillBlockFromText{Name: "deploy", Location: "/skills/deploy/SKILL.md", Content: "References are relative to /skills/deploy.\n\nbody"}},
		{"user message is trimmed", block + "\n\n  hello \n", &ParsedSkillBlockFromText{Name: "deploy", Location: "/skills/deploy/SKILL.md", Content: "References are relative to /skills/deploy.\n\nbody", UserMessage: "hello"}},
		// JS trim strips U+FEFF and keeps U+0085; Go's strings.TrimSpace does the opposite.
		{"BOM-only user message is undefined", block + "\n\n\uFEFF", &ParsedSkillBlockFromText{Name: "deploy", Location: "/skills/deploy/SKILL.md", Content: "References are relative to /skills/deploy.\n\nbody"}},
		{"NEL survives the trim", block + "\n\n\u0085x\u0085", &ParsedSkillBlockFromText{Name: "deploy", Location: "/skills/deploy/SKILL.md", Content: "References are relative to /skills/deploy.\n\nbody", UserMessage: "\u0085x\u0085"}},
		{"lazy body stops at the first closing tag", block + "\n\nnote\n</skill>", &ParsedSkillBlockFromText{Name: "deploy", Location: "/skills/deploy/SKILL.md", Content: "References are relative to /skills/deploy.\n\nbody", UserMessage: "note\n</skill>"}},
		{"no match: leading text", "x" + block, nil},
		{"no match: trailing text without separator", block + "\nx", nil},
		{"no match: empty name", "<skill name=\"\" location=\"/p\">\nb\n</skill>", nil},
	} {
		got := ParseSkillBlock(tc.text)
		if (got == nil) != (tc.want == nil) || got != nil && *got != *tc.want {
			t.Errorf("%s: got %+v, want %+v", tc.name, got, tc.want)
		}
	}
}
