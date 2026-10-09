package tui

import (
	"strings"
	"testing"
	"time"
)

// Pi 1.1.0 packages/coding-agent/src/modes/interactive/components/tool-execution.ts:207 setOutputPad(outputPad) stores the padding
// and runs updateDisplay, which gives the content box (line 283, renderContainer.setPaddingX) and the default text card (line 335)
// that horizontal padding; the constructor takes it from options.outputPad (line 81, default 1).
func TestToolExecutionSetOutputPadMatchesPi(t *testing.T) {
	indent := func(c *ToolExecutionComponent, needle string) int {
		t.Helper()
		for _, line := range c.Render(60) {
			if plain := stripANSI(line); strings.Contains(plain, needle) {
				return len(plain) - len(strings.TrimLeft(plain, " "))
			}
		}
		t.Fatalf("no row with %q in %q", needle, c.Render(60))
		return -1
	}
	zero := 0
	for name, c := range map[string]*ToolExecutionComponent{
		"default":     newToolCardForTest("grep", "pattern"),
		"constructor": newToolCardForTest("grep", "pattern", ToolExecutionOptions{OutputPad: &zero}),
	} {
		c.SetResult("hit", false, time.Second)
		want := 1
		if name == "constructor" {
			want = 0
		}
		if got := indent(c, "hit"); got != want {
			t.Fatalf("%s: output indent = %d, want %d", name, got, want)
		}
		c.SetOutputPad(1)
		if got := indent(c, "hit"); got != 1 {
			t.Fatalf("%s: SetOutputPad(1) indent = %d", name, got)
		}
		c.SetOutputPad(0)
		if got := indent(c, "hit"); got != 0 {
			t.Fatalf("%s: SetOutputPad(0) indent = %d", name, got)
		}
	}
}

// tool-execution.ts:283 renderContainer.setPaddingX(this.outputPad): a tool with a renderer definition draws its call component
// inside the content Box, so the Box padding follows setOutputPad.
func TestToolExecutionDefinitionBoxFollowsOutputPad(t *testing.T) {
	card := newToolCardForTest("probe", "")
	card.SetDefinition(&ToolDefinitionRenderers{
		Call: func(ToolRenderInput) (Component, bool) { return NewPaddedText("CALL", 0, 0, nil), true },
	}, []byte(`{}`))
	indent := func() int {
		for _, line := range card.Render(30) {
			if plain := stripANSI(line); strings.Contains(plain, "CALL") {
				return len(plain) - len(strings.TrimLeft(plain, " "))
			}
		}
		t.Fatal("no CALL row")
		return -1
	}
	if got := indent(); got != 1 {
		t.Fatalf("default: indent = %d, want 1", got)
	}
	card.SetOutputPad(0)
	if got := indent(); got != 0 {
		t.Fatalf("SetOutputPad(0): indent = %d", got)
	}
}
