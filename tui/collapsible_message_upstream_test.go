package tui

// pi: packages/coding-agent/src/modes/interactive/components/skill-invocation-message.ts

// pi: packages/coding-agent/src/modes/interactive/components/compaction-summary-message.ts

// pi: packages/coding-agent/src/modes/interactive/components/branch-summary-message.ts

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
		{"compaction", "compaction details", NewCompactionSummaryMessageComponent(CompactionSummaryMessage{Summary: "compaction details", TokensBefore: 1234}, nil, 1)},
		{"branch", "branch details", NewBranchSummaryMessageComponent(BranchSummaryMessage{Summary: "branch details"}, nil, 1)},
		{"skill", "skill details", NewSkillInvocationMessageComponent(ParsedSkillBlock{Name: "example-skill", Content: "skill details"}, nil, 1)},
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
			component := NewCompactionSummaryMessageComponent(CompactionSummaryMessage{Summary: strings.Repeat("details\n", lines), TokensBefore: 1234}, nil, 1)
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
		{"toggles a compaction summary when clicked", "[compaction]", "compaction details", NewCompactionSummaryMessageComponent(CompactionSummaryMessage{Summary: "compaction details", TokensBefore: 1234}, nil, 1)},
		// .upstream/v0.87.1/packages/coding-agent/test/collapsible-message-components.test.ts:55
		{"toggles a branch summary when clicked", "[branch]", "branch details", NewBranchSummaryMessageComponent(BranchSummaryMessage{Summary: "branch details"}, nil, 1)},
		// .upstream/v0.87.1/packages/coding-agent/test/collapsible-message-components.test.ts:71
		{"toggles a skill invocation when clicked", "[skill]", "skill details", NewSkillInvocationMessageComponent(ParsedSkillBlock{Name: "example-skill", Content: "skill details"}, nil, 1)},
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

// upstream: compaction-summary-message.ts and branch-summary-message.ts `extends Box`: setBgFn replaces the background of
// the rendered box and clear() empties it, as for any Box.
func TestSummaryMessageComponentsInheritBoxMembers(t *testing.T) {
	for name, component := range map[string]interface {
		Component
		SetBgFn(func(string) string)
		Clear()
	}{
		"compaction": NewCompactionSummaryMessageComponent(CompactionSummaryMessage{Summary: "details", TokensBefore: 1}, nil, 1),
		"branch":     NewBranchSummaryMessageComponent(BranchSummaryMessage{Summary: "details"}, nil, 1),
	} {
		t.Run(name, func(t *testing.T) {
			component.SetBgFn(func(text string) string { return "<bg>" + text + "</bg>" })
			if got := strings.Join(component.Render(40), "\n"); !strings.Contains(got, "<bg>") {
				t.Errorf("SetBgFn did not reach the rendered box:\n%s", got)
			}
			component.Clear()
			if got := strings.Join(component.Render(40), "\n"); strings.Contains(got, "to expand") {
				t.Errorf("Clear left the summary rendered:\n%s", got)
			}
		})
	}
}
