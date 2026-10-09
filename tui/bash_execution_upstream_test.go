package tui

import (
	"slices"
	"strings"
	"testing"
)

// upstreamBashRows builds the rows upstream BashExecutionComponent draws after updateDisplay
// (.upstream/current/packages/coding-agent/src/modes/interactive/components/bash-execution.ts:119-205): a spacer, the
// border, the header in "bashMode", the output Text or visual preview, the status Text, and the border, every row a
// Text row padded to the width.
func upstreamBashRows(width int, colorKey, command string, output []string, status []string) []string {
	th := ActiveTheme()
	border := th.FgText(colorKey, strings.Repeat("─", width))
	rows := []string{"", border}
	rows = append(rows, NewPaddedText(th.FgText("bashMode", "\x1b[1m$ "+command+"\x1b[22m"), 1, 0, nil).Render(width)...)
	if len(output) > 0 {
		styled := make([]string, len(output))
		for i, line := range output {
			styled[i] = th.FgText("muted", line)
		}
		rows = append(rows, TruncateToVisualLines("\n"+strings.Join(styled, "\n"), 20, width, 1).VisualLines...)
	}
	if len(status) > 0 {
		rows = append(rows, NewPaddedText("\n"+strings.Join(status, "\n"), 1, 0, nil).Render(width)...)
	}
	return append(rows, border)
}

func TestBashExecutionBlockMatchesUpstreamComponent(t *testing.T) {
	th := ActiveTheme()
	two, zero := 2, 0
	hint := th.FgText("muted", "... 5 more lines (") + KeyHint(AppKeyText("app.tools.expand", "ctrl+o"), "to expand") + th.FgText("muted", ")")
	lines := make([]string, 25)
	for i := range lines {
		lines[i] = strings.Repeat("x", i+1)
	}
	for _, tc := range []struct {
		name  string
		block func() *BashExecutionBlock
		want  []string
	}{
		{"exit status in the theme's error color", func() *BashExecutionBlock {
			b := NewBashExecutionBlock("false", false)
			b.AppendOutput("oops")
			b.SetComplete(&two, false, false)
			return b
		}, upstreamBashRows(50, "bashMode", "false", []string{"oops"}, []string{th.FgText("error", "(exit 2)")})},
		{"cancelled in the theme's warning color", func() *BashExecutionBlock {
			b := NewBashExecutionBlock("sleep 9", false)
			b.SetComplete(nil, true, false)
			return b
		}, upstreamBashRows(50, "bashMode", "sleep 9", nil, []string{th.FgText("warning", "(cancelled)")})},
		{"a collapsed preview with the expand key hint", func() *BashExecutionBlock {
			b := NewBashExecutionBlock("seq", false)
			b.AppendOutput(strings.Join(lines, "\n"))
			b.SetComplete(&zero, false, false)
			return b
		}, upstreamBashRows(50, "bashMode", "seq", lines[5:], []string{hint})},
		{"an excluded command's header turns bashMode once the output arrives, its borders stay dim", func() *BashExecutionBlock {
			b := NewBashExecutionBlock("secret", true)
			b.SetCompleteWithOutput(&zero, false, true, "x\r\ny\x1b[31m!\x1b[0m", "/tmp/full.log")
			return b
		}, upstreamBashRows(50, "dim", "secret", []string{"x", "y!"}, []string{th.FgText("warning", "Output truncated. Full output: /tmp/full.log")})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.block().Render(50); !slices.Equal(got, tc.want) {
				t.Fatalf("rows differ from upstream:\n got %q\nwant %q", got, tc.want)
			}
		})
	}
}

// Before updateDisplay first runs, upstream's constructor drew the header in the command's own color and the spinner
// with keyText("tui.select.cancel") (bash-execution.ts:32-65).
func TestBashExecutionBlockRunningMatchesUpstreamConstructor(t *testing.T) {
	th := ActiveTheme()
	b := NewBashExecutionBlock("secret", true)
	loader := NewLoader("Running... (escape/ctrl+c to cancel)")
	loader.SpinnerColorFn = func(text string) string { return th.FgText("dim", text) }
	loader.MessageColorFn = func(text string) string { return th.FgText("muted", text) }
	border := th.FgText("dim", strings.Repeat("─", 40))
	want := []string{"", border}
	want = append(want, NewPaddedText(th.FgText("dim", "\x1b[1m$ secret\x1b[22m"), 1, 0, nil).Render(40)...)
	want = append(want, loader.Render(40)...)
	want = append(want, border)
	if got := b.Render(40); !slices.Equal(got, want) {
		t.Fatalf("running rows differ from upstream:\n got %q\nwant %q", got, want)
	}
}
