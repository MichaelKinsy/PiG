package tui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

func BenchmarkBashLongCommandHeader(b *testing.B) {
	block := NewBashExecutionBlock("echo "+strings.Repeat("argument ", 64), false)
	block.SetComplete(new(0), false, false)
	b.ReportAllocs()
	for b.Loop() {
		block.Render(100)
	}
}

func TestBashCommandHeaderUsesPaddedTextLayout(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/src/modes/interactive/components/bash-execution.ts:51,138.
	for _, command := range []string{"", "echo short", "echo " + strings.Repeat("value ", 30), strings.Repeat("界🙂", 20), "printf first\nprintf second"} {
		for _, width := range []int{12, 40, 100} {
			block := NewBashExecutionBlock(command, false)
			block.SetComplete(new(0), false, false)
			lines := block.Render(width)
			header := lines[2 : len(lines)-1]
			want := NewPaddedText(ActiveTheme().FgText("bashMode", "\x1b[1m$ "+command+"\x1b[22m"), 1, 0, nil).Render(width)
			if !reflect.DeepEqual(header, want) {
				t.Errorf("command=%q width=%d header=%q want=%q", command, width, header, want)
			}
			for _, line := range lines {
				if widthx.VisibleWidth(line) > width {
					t.Fatalf("command=%q width=%d overflow=%q", command, width, line)
				}
			}
		}
	}
}
