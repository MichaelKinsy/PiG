package tui

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// Pi 1.1.0's custom-message.ts:90 sets the default box's horizontal padding to outputPad on every rebuild (1.0.4 kept Box(1,1)).
func TestCustomMessageDefaultBoxFollowsOutputPad(t *testing.T) {
	c := NewCustomMessageComponent(&CustomMessage{CustomType: "notice", Content: "custom"}, nil, nil, 1)
	before := c.Render(40)
	c.SetOutputPad(0)
	after := c.Render(40)
	if reflect.DeepEqual(before, after) {
		t.Fatalf("default box ignored the output pad: %q", after)
	}
	if got := stripANSI(after[2]); !strings.HasPrefix(got, "[notice]") {
		t.Fatalf("pad 0 label row = %q, want no inset", got)
	}
	if got := stripANSI(before[2]); !strings.HasPrefix(got, " [notice]") {
		t.Fatalf("pad 1 label row = %q, want one column of inset", got)
	}
}

// Port of assistant-message.test.ts's configured output padding case, including
// hidden thinking and terminal-error text, all of which use the same inset.
// Pi: packages/coding-agent/src/modes/interactive/components/assistant-message.ts:73 (AssistantMessageComponent.setOutputPad).
func TestAssistantMessageConfiguredOutputPadding(t *testing.T) {
	for _, hidden := range []bool{false, true} {
		t.Run(fmt.Sprint(hidden), func(t *testing.T) {
			c := NewAssistantMessageComponent(nil, hidden, nil, "", nil, nil)
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
