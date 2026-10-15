package tui

import (
	"strings"
	"testing"
)

// Upstream skill-invocation-message.ts setExpanded(expanded) stores the flag and rebuilds the display: collapsed shows
// "[skill] name" with an expand hint, expanded adds the skill name header and the whole content.
func TestSkillInvocationSetExpandedSwitchesTheDisplay(t *testing.T) {
	c := NewSkillInvocationMessageComponent(ParsedSkillBlock{Name: "example-skill", Content: "line one\nline two"}, nil, 1)
	text := func() string { return stripANSI(strings.Join(c.Render(80), "\n")) }
	if got := text(); !strings.Contains(got, "[skill] example-skill") || strings.Contains(got, "line one") {
		t.Fatalf("collapsed display: %q", got)
	}
	c.SetExpanded(true)
	if got := text(); !strings.Contains(got, "line one") || !strings.Contains(got, "line two") || strings.Contains(got, "to expand") {
		t.Fatalf("expanded display: %q", got)
	}
	c.SetExpanded(false)
	if got := text(); strings.Contains(got, "line one") || !strings.Contains(got, "to expand") {
		t.Fatalf("collapsed again: %q", got)
	}
}

// Pi skill-invocation-message.ts:14-44: the component is a Box(1, 1) and its expanded body is rendered with the markdown theme given to the constructor.
func TestSkillInvocationMessageIsABoxAndUsesTheInjectedMarkdownTheme(t *testing.T) {
	theme := GetMarkdownTheme()
	theme.Bold = func(text string) string { return "<<" + text + ">>" }
	c := NewSkillInvocationMessageComponent(ParsedSkillBlock{Name: "example-skill", Content: "body"}, &theme, 1)
	if c.PaddingX != 1 || c.PaddingY != 1 || len(c.children) != 1 {
		t.Fatalf("box padding %d,%d children %d", c.PaddingX, c.PaddingY, len(c.children))
	}
	c.SetExpanded(true)
	if len(c.children) != 1 {
		t.Fatalf("each display update clears the box: children %d", len(c.children))
	}
	if joined := strings.Join(c.Render(60), "\n"); !strings.Contains(stripANSI(joined), "<<example-skill>>") {
		t.Fatalf("the injected markdown theme must style the bold skill name:\n%s", joined)
	}
	if joined := strings.Join(NewSkillInvocationMessageComponent(ParsedSkillBlock{Name: "example-skill", Content: "body"}, nil, 1).Render(60), "\n"); strings.Contains(joined, "<<") {
		t.Fatal("the default theme must not be the injected one")
	}
}

// Pi skill-invocation-message.ts:16-17: the constructor's outputPad is the Box's horizontal padding (default 1), and a nil markdown theme is the default theme.
func TestSkillInvocationConstructorOutputPadIsTheBoxPadding(t *testing.T) {
	block := ParsedSkillBlock{Name: "example-skill", Content: "body"}
	for _, pad := range []int{0, 1, 3} {
		c := NewSkillInvocationMessageComponent(block, nil, pad)
		if c.PaddingX != pad {
			t.Fatalf("outputPad %d gave PaddingX %d", pad, c.PaddingX)
		}
		line := stripANSI(c.Render(40)[1]) // line 0 is the vertical padding row
		if !strings.HasPrefix(line, strings.Repeat(" ", pad)+"[skill]") {
			t.Fatalf("outputPad %d rendered %q", pad, line)
		}
	}
}
