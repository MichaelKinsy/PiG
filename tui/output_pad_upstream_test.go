package tui

import (
	"strings"
	"testing"
)

func leadingSpaces(line string) int {
	plain := stripANSI(line)
	return len(plain) - len(strings.TrimLeft(plain, " "))
}

// Pi 1.1.0 packages/tui/src/components/box.ts:53 setPaddingX(paddingX) sets paddingX and drops the render cache, so the next
// render of the same Box lays its children out with the new left and right padding.
func TestBoxSetPaddingXMatchesPi(t *testing.T) {
	box := NewPaddedBox(2, 0, nil)
	box.AddChild(NewText("hi"))
	if got := leadingSpaces(box.Render(20)[0]); got != 2 {
		t.Fatalf("padding 2: leading spaces = %d", got)
	}
	box.SetPaddingX(0)
	if got := leadingSpaces(box.Render(20)[0]); got != 0 {
		t.Fatalf("after SetPaddingX(0): leading spaces = %d", got)
	}
	box.SetPaddingX(3)
	if got := leadingSpaces(box.Render(20)[0]); got != 3 {
		t.Fatalf("after SetPaddingX(3): leading spaces = %d", got)
	}
}

// Pi 1.1.0 packages/tui/src/components/text.ts:39 setPaddingX(paddingX) sets paddingX and invalidates the cached lines.
func TestTextSetPaddingXMatchesPi(t *testing.T) {
	text := NewPaddedText("hi", 1, 0, nil)
	if got := leadingSpaces(text.Render(20)[0]); got != 1 {
		t.Fatalf("padding 1: leading spaces = %d", got)
	}
	text.SetPaddingX(4)
	if got := leadingSpaces(text.Render(20)[0]); got != 4 {
		t.Fatalf("after SetPaddingX(4): leading spaces = %d", got)
	}
}

// Pi 1.1.0 packages/tui/src/components/loader.ts: Loader extends Text, so setPaddingX (text.ts:39) changes the padding of the
// spinner line (render() prepends one empty line, loader.ts) and CancellableLoader, which extends Loader, inherits it.
func TestLoaderSetPaddingXMatchesPi(t *testing.T) {
	loader := NewLoader(nil, nil, nil, "working", nil)
	cancellable := NewCancellableLoader(nil, nil, nil, "working", nil)
	for _, tc := range []struct {
		name   string
		render func(int) []string
		set    func(int)
	}{
		{"loader", loader.Render, loader.SetPaddingX},
		{"cancellable", cancellable.Render, cancellable.SetPaddingX},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := leadingSpaces(tc.render(30)[1]); got != 1 {
				t.Fatalf("default padding: leading spaces = %d, want 1", got)
			}
			for _, x := range []int{0, 3} {
				tc.set(x)
				if got := leadingSpaces(tc.render(30)[1]); got != x {
					t.Fatalf("SetPaddingX(%d): leading spaces = %d", x, got)
				}
			}
		})
	}
}

// Pi 1.1.0 packages/coding-agent/src/modes/interactive/components/branch-summary-message.ts:27 (also
// compaction-summary-message.ts:27 and skill-invocation-message.ts:28): setOutputPad(outputPad) is setPaddingX(outputPad) on the
// message's Box, so the label row moves with the output padding.
func TestSummaryMessageComponentsSetOutputPadMatchesPi(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build func() interface {
			Component
			SetOutputPad(int)
		}
	}{
		{"branch summary", func() interface {
			Component
			SetOutputPad(int)
		} {
			return NewBranchSummaryMessageComponent(BranchSummaryMessage{Summary: "explored a sidebar"}, nil, 1)
		}},
		{"compaction summary", func() interface {
			Component
			SetOutputPad(int)
		} {
			return NewCompactionSummaryMessageComponent(CompactionSummaryMessage{Summary: "kept the plan", TokensBefore: 1200}, nil, 1)
		}},
		{"skill invocation", func() interface {
			Component
			SetOutputPad(int)
		} {
			return NewSkillInvocationMessageComponent(ParsedSkillBlock{Name: "review", Content: "check it"}, nil, 1)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			component := tc.build()
			labelPad := func() int {
				for _, line := range component.Render(60) {
					if plain := stripANSI(line); strings.Contains(plain, "[") {
						return leadingSpaces(line)
					}
				}
				t.Fatal("no label row")
				return -1
			}
			if got := labelPad(); got != 1 {
				t.Fatalf("default output padding: label indent = %d, want 1", got)
			}
			component.SetOutputPad(0)
			if got := labelPad(); got != 0 {
				t.Fatalf("SetOutputPad(0): label indent = %d", got)
			}
			component.SetOutputPad(1)
			if got := labelPad(); got != 1 {
				t.Fatalf("SetOutputPad(1): label indent = %d", got)
			}
		})
	}
}
