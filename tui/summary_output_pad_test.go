package tui

import (
	"strings"
	"testing"
)

// leadingColumns is the number of blank columns before the first visible character of the row that holds label.
func leadingColumns(t *testing.T, component Component, label string) int {
	t.Helper()
	for _, line := range component.Render(60) {
		plain := stripANSI(line)
		if strings.Contains(plain, label) {
			return len(plain) - len(strings.TrimLeft(plain, " "))
		}
	}
	t.Fatalf("no row contains %q", label)
	return -1
}

// Pi packages/coding-agent/src/modes/interactive/components/compaction-summary-message.ts:15-29 (constructor outputPad = 1,
// setOutputPad -> Box.setPaddingX, box.ts:53), branch-summary-message.ts:15-28 and skill-invocation-message.ts:16-29: the
// horizontal padding of each message box is the outputPad given at construction and follows setOutputPad.
func TestMessageBoxesFollowOutputPad(t *testing.T) {
	cases := []struct {
		name  string
		label string
		build func(pad int) Component
	}{
		{"compaction summary", "[compaction]", func(pad int) Component {
			message := CompactionSummaryMessage{Summary: "s", TokensBefore: 1}
			c := NewCompactionSummaryMessageComponent(message, nil, 1)
			c.SetOutputPad(pad)
			return c
		}},
		{"branch summary", "[branch]", func(pad int) Component {
			c := NewBranchSummaryMessageComponent(BranchSummaryMessage{Summary: "s"}, nil, 1)
			c.SetOutputPad(pad)
			return c
		}},
		{"skill invocation", "[skill]", func(pad int) Component {
			block := ParsedSkillBlock{Name: "n", Content: "c"}
			c := NewSkillInvocationMessageComponent(block, nil, 1)
			c.SetOutputPad(pad)
			return c
		}},
	}
	for _, tc := range cases {
		for _, pad := range []int{0, 1} {
			if got := leadingColumns(t, tc.build(pad), tc.label); got != pad {
				t.Errorf("%s SetOutputPad(%d): label starts after %d blank columns, want %d", tc.name, pad, got, pad)
			}
		}
	}
}

// Pi tool-execution.ts:28,81,207,283,335 (ToolExecutionOptions.outputPad ?? 1, setOutputPad, Box/Text setPaddingX(outputPad)) and
// bash-execution.ts:35,73,141 (constructor outputPad, setOutputPad, header Text(.., outputPad, 0)): the tool card and the bash
// block pad their rows by the outputPad given at construction and by the latest setOutputPad.
func TestToolAndBashBlocksFollowOutputPad(t *testing.T) {
	for _, pad := range []int{0, 1} {
		built := newToolCardForTest("custom_tool", "tool-arg-marker", ToolExecutionOptions{OutputPad: &pad})
		if got := leadingColumns(t, built, "tool-arg-marker"); got != pad {
			t.Errorf("tool card constructed with outputPad %d: header starts after %d columns", pad, got)
		}
		updated := newToolCardForTest("custom_tool", "tool-arg-marker")
		updated.SetOutputPad(pad)
		if got := leadingColumns(t, updated, "tool-arg-marker"); got != pad {
			t.Errorf("tool card SetOutputPad(%d): header starts after %d columns", pad, got)
		}
		late := NewBashExecutionComponent("echo bash-marker", nil, false, 1)
		late.SetOutputPad(pad)
		if got := leadingColumns(t, late, "echo bash-marker"); got != pad {
			t.Errorf("bash block SetOutputPad(%d): command starts after %d columns", pad, got)
		}
	}
}
