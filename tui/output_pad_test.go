package tui

import (
	"slices"
	"strings"
	"testing"
)

// pi 1.1.0 box.ts and text.ts setPaddingX (#10557): the new padding applies to the next render, not to a cached one.
func TestSetPaddingXDropsRenderCache(t *testing.T) {
	t.Run("Text", func(t *testing.T) {
		text := NewPaddedText("hello", 0, 0, nil)
		if got := plainLines(text.Render(20)); !slices.Equal(got, []string{"hello"}) {
			t.Fatalf("outputPad 0 = %q", got)
		}
		text.SetPaddingX(2)
		if got := plainLines(text.Render(20)); !slices.Equal(got, []string{"  hello"}) {
			t.Fatalf("after SetPaddingX(2) = %q", got)
		}
		text.SetPaddingX(0)
		if got := plainLines(text.Render(20)); !slices.Equal(got, []string{"hello"}) {
			t.Fatalf("after SetPaddingX(0) = %q", got)
		}
	})
	t.Run("Box", func(t *testing.T) {
		box := NewPaddedBox(0, 0, nil)
		box.AddChild(NewText("hello"))
		if got := plainLines(box.Render(20)); !slices.Equal(got, []string{"hello"}) {
			t.Fatalf("padding 0 = %q", got)
		}
		box.SetPaddingX(2)
		if got := plainLines(box.Render(20)); !slices.Equal(got, []string{"  hello"}) {
			t.Fatalf("after SetPaddingX(2) = %q", got)
		}
	})
	// A parent container re-renders a child only after the child invalidates itself.
	t.Run("inside a Container", func(t *testing.T) {
		box := NewPaddedBox(0, 0, nil)
		box.AddChild(NewText("hello"))
		text := NewPaddedText("world", 0, 0, nil)
		container := NewContainer()
		container.Add(box)
		container.Add(text)
		if got := plainLines(container.Render(20)); !slices.Equal(got, []string{"hello", "world"}) {
			t.Fatalf("padding 0 = %q", got)
		}
		box.SetPaddingX(1)
		text.SetPaddingX(1)
		if got := plainLines(container.Render(20)); !slices.Equal(got, []string{" hello", " world"}) {
			t.Fatalf("after SetPaddingX(1) = %q", got)
		}
	})
}

// pi 1.1.0 bash-execution.ts keeps the `!!` header in the dim color once output arrives and the block completes (#10557): updateDisplay
// colors the header with the same key as the border and spinner instead of always using bashMode.
func TestBashExecutionExcludedHeaderKeepsDimColor(t *testing.T) {
	theme := ActiveTheme()
	headerRow := func(block *BashExecutionComponent) string {
		for _, row := range block.Render(60) {
			if strings.Contains(stripANSI(row), "$ pwd") {
				return row
			}
		}
		t.Fatal("no header row")
		return ""
	}
	for _, tc := range []struct {
		name               string
		excludeFromContext bool
		want, other        string
	}{
		{"!! command", true, theme.Dim, theme.BashMode},
		{"! command", false, theme.BashMode, theme.Dim},
	} {
		t.Run(tc.name, func(t *testing.T) {
			block := NewBashExecutionComponent("pwd", nil, tc.excludeFromContext, 1)
			if row := headerRow(block); !strings.Contains(row, tc.want) || strings.Contains(row, tc.other) {
				t.Fatalf("running header = %q, want color %q only", row, tc.want)
			}
			block.AppendOutput("/tmp\n")
			exitCode := 0
			block.SetComplete(&exitCode, false, nil, "")
			if row := headerRow(block); !strings.Contains(row, tc.want) || strings.Contains(row, tc.other) {
				t.Fatalf("completed header = %q, want color %q only", row, tc.want)
			}
		})
	}
}
