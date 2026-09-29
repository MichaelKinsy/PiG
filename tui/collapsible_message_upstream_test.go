package tui

import (
	"fmt"
	"strings"
	"testing"
)

func TestCollapsibleMessageClicksThroughTerminal(t *testing.T) {
	for _, tc := range []struct {
		name, details string
		component     Component
	}{
		{"compaction", "compaction details", NewCompactionSummaryComponent("compaction details", 1234)},
		{"branch", "branch details", NewBranchSummaryComponent("branch details")},
		{"skill", "skill details", NewSkillInvocationMessage(ParsedSkillBlock{Name: "example-skill", Content: "skill details"})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newAltHarness(t, 80, 24, TuiAltScreenOptions{})
			h.tui.Add(tc.component)
			h.start()
			for _, want := range []bool{true, false} {
				h.send("\x1b[<0;3;2M", "\x1b[<0;3;2m")
				if got := strings.Contains(strings.Join(tc.component.Render(80), "\n"), tc.details); got != want {
					t.Fatalf("terminal click detail visibility = %v, want %v", got, want)
				}
			}
		})
	}
}

// Click dispatch is constant work in the stored summary length; only the next frame renders it.
func BenchmarkCollapsibleMessageClick(b *testing.B) {
	for _, lines := range []int{1, 10000} {
		b.Run(fmt.Sprint(lines), func(b *testing.B) {
			component := NewCompactionSummaryComponent(strings.Repeat("details\n", lines), 1234)
			event := componentMouseEvent(MouseClick, 2, 1)
			event.Height = 5
			b.ReportAllocs()
			for b.Loop() {
				DispatchMouseEvent(component, event)
			}
		})
	}
}

func TestCollapsibleMessageComponentsUpstream(t *testing.T) {
	cases := []struct {
		name, marker, details string
		component             Component
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/collapsible-message-components.test.ts:39
		{"toggles a compaction summary when clicked", "[compaction]", "compaction details", NewCompactionSummaryComponent("compaction details", 1234)},
		// .upstream/v0.87.1/packages/coding-agent/test/collapsible-message-components.test.ts:55
		{"toggles a branch summary when clicked", "[branch]", "branch details", NewBranchSummaryComponent("branch details")},
		// .upstream/v0.87.1/packages/coding-agent/test/collapsible-message-components.test.ts:71
		{"toggles a skill invocation when clicked", "[skill]", "skill details", NewSkillInvocationMessage(ParsedSkillBlock{Name: "example-skill", Content: "skill details"})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			height := len(tc.component.Render(80))
			for _, event := range []TuiMouseEvent{
				{Type: MouseClick, Button: MouseButtonLeft, X: 2, Y: 0},
				{Type: MouseClick, Button: MouseButtonLeft, X: 0, Y: 1},
				{Type: MouseClick, Button: MouseButtonLeft, X: 79, Y: 1},
				{Type: MouseClick, Button: MouseButtonLeft, X: 2, Y: height - 1},
				{Type: MousePress, Button: MouseButtonLeft, X: 2, Y: 1},
				{Type: MouseClick, Button: MouseButtonRight, X: 2, Y: 1},
			} {
				event.Width, event.Height = 80, height
				if result := DispatchMouseEvent(tc.component, event); result != nil {
					t.Fatalf("padding/non-left-click handled: %+v", event)
				}
			}
			for step, expanded := range []bool{false, true, false} {
				lines := tc.component.Render(80)
				if got := strings.Contains(stripANSI(strings.Join(lines, "\n")), tc.details); got != expanded {
					t.Fatalf("details visible = %v, want %v: %q", got, expanded, lines)
				}
				if step == 2 {
					break
				}
				row := -1
				for i, line := range lines {
					if strings.Contains(stripANSI(line), tc.marker) {
						row = i
						break
					}
				}
				if row < 0 {
					t.Fatal("missing marker")
				}
				event := componentMouseEvent(MouseClick, 2, row)
				event.Height = len(lines)
				if result := DispatchMouseEvent(tc.component, event); result == nil || !result.Handled {
					t.Fatal("click was not handled")
				}
			}
		})
	}
}
