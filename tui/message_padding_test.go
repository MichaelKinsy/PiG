package tui

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// Pi's custom-message.ts uses Box(1,1) for the default renderer regardless of
// outputPad. Only registered renderers consume that option.
func TestCustomMessageDefaultOutputPadKeepsBoxInset(t *testing.T) {
	c := NewCustomMessageComponent("notice", "custom")
	before := c.Render(40)
	c.SetOutputPad(0)
	if after := c.Render(40); !reflect.DeepEqual(before, after) {
		t.Fatalf("default box changed: before=%q after=%q", before, after)
	}
}

// Port of assistant-message.test.ts's configured output padding case, including
// hidden thinking and terminal-error text, all of which use the same inset.
func TestAssistantMessageConfiguredOutputPadding(t *testing.T) {
	for _, hidden := range []bool{false, true} {
		t.Run(fmt.Sprint(hidden), func(t *testing.T) {
			c := NewAssistantMessageBlock(hidden)
			c.SetTextDelta("hello")
			c.SetThinkingDelta("reasoning")
			c.SetTerminalError("error", "failure")
			for _, padding := range []int{1, 0, 1} {
				c.SetOutputPad(padding)
				for _, word := range []string{"hello", "Error: failure", map[bool]string{false: "reasoning", true: "Thinking..."}[hidden]} {
					found := false
					for _, line := range c.Render(80) {
						if strings.HasPrefix(stripANSI(line), strings.Repeat(" ", padding)+word) {
							found = true
						}
					}
					if !found {
						t.Fatalf("padding %d: missing %q in %q", padding, word, c.Render(80))
					}
				}
			}
		})
	}
}
