package tui

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Node encodes a string's lone UTF-16 surrogates as U+FFFD when process.stdout.write runs; every way PiG writes to a ProcessTerminal does too,
// and does it once: a write through a renderer's writer over a ProcessTerminal is encoded by ProcessTerminal.Write alone.
func TestProcessTerminalWritesEncodeLoneSurrogates(t *testing.T) {
	text := "a" + jsstring.FromUTF16([]uint16{0xd83d}) + "b"
	want := "a\ufffdb"
	for _, tc := range []struct {
		name  string
		write func(term *ProcessTerminal, out io.Writer)
	}{
		{"Write", func(term *ProcessTerminal, _ io.Writer) { term.Write(text) }},
		{"writeUnlogged", func(term *ProcessTerminal, _ io.Writer) { term.writeUnlogged(text) }},
		{"RestoreOnExit", func(term *ProcessTerminal, _ io.Writer) { term.RestoreOnExit(text, "") }},
		{"WriteToTerminal over the terminal's writer", func(term *ProcessTerminal, _ io.Writer) { WriteToTerminal(terminalWriter{term}, text) }},
		{"WriteToTerminal over the terminal's cursor writer", func(term *ProcessTerminal, _ io.Writer) { WriteToTerminal(terminalCursorWriter{term}, text) }},
		{"WriteToTerminal over a plain writer", func(_ *ProcessTerminal, out io.Writer) { WriteToTerminal(out, text) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			tc.write(NewProcessTerminalWithOutput(nil, nil, &out), &out)
			if got := out.String(); !strings.Contains(got, want) || strings.Contains(got, "\xed") {
				t.Fatalf("wrote %q, want it to carry %q and no WTF-8 byte", got, want)
			}
		})
	}
}

// BenchmarkTerminalWriteThroughProcessTerminal is the production write path of a frame chunk, WriteToTerminal over a renderer's writer over a
// ProcessTerminal, for a chunk of ASCII and of CJK text (valid UTF-8 beyond ASCII, which a scan for a lone surrogate must walk). The chunk must be
// encoded once: ProcessTerminal.Write alone scans it.
func BenchmarkTerminalWriteThroughProcessTerminal(b *testing.B) {
	rows := map[string]string{
		"ascii": "\x1b[38;2;200;200;200m" + strings.Repeat("token ", 18) + "\x1b[0m\x1b]8;;\a\r\n",
		"cjk":   "\x1b[38;2;200;200;200m" + strings.Repeat("日本語のテキスト ", 6) + "\x1b[0m\x1b]8;;\a\r\n",
	}
	for _, size := range []int{512, 64 << 10, 2 << 20} {
		for _, kind := range []string{"ascii", "cjk"} {
			frame := strings.ToValidUTF8(strings.Repeat(rows[kind], size/len(rows[kind])+1)[:size], "")
			b.Run(fmt.Sprintf("%s/%d", kind, size), func(b *testing.B) {
				out := terminalWriter{NewProcessTerminalWithOutput(nil, nil, io.Discard)}
				b.SetBytes(int64(len(frame)))
				b.ReportAllocs()
				for b.Loop() {
					WriteToTerminal(out, frame)
				}
			})
		}
	}
}

// BenchmarkProcessTerminalFullRedrawCJK is a full redraw (a change above the viewport) of a CJK transcript through a ProcessTerminal.
func BenchmarkProcessTerminalFullRedrawCJK(b *testing.B) {
	b.Setenv("COLUMNS", "120")
	b.Setenv("LINES", "40")
	b.Setenv("PI_TUI_WRITE_LOG", "")
	for _, turns := range []int{500, 5000} {
		b.Run(fmt.Sprintf("turns=%d", turns), func(b *testing.B) {
			ui := NewTuiMainScreen(NewProcessTerminalWithOutput(nil, nil, io.Discard), nil, b.TempDir())
			top := NewText("\033[2m● first turn marker\033[0m")
			ui.Add(top)
			for i := range turns {
				ui.Add(NewUserMessageComponent(fmt.Sprintf("refactor the %d-th handler and keep the tests green", i), nil, 1, nil, "", ""))
				ui.Add(NewMarkdown(fmt.Sprintf("第%d回の変更です。`pkg/foo` と `pkg/bar` に触れます。\n\n- 変更についての最初の要点\n- より詳しい二番目の要点\n- 三番目のトレードオフ ─── ✓\n\n```go\nfunc handler%d() error { return nil } // 日本語のコメント\n```\n", i, i)))
			}
			ui.Render()
			b.ReportAllocs()
			i := 0
			for b.Loop() {
				i++
				top.SetText(fmt.Sprintf("\033[2m● marker %06d\033[0m", i))
				ui.Render()
			}
		})
	}
}
