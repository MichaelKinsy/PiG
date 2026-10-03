package codingagent

import (
	"bytes"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// upstream 0.99.1 packages/tui/test/terminal-colors.test.ts (TUI.queryTerminalColors): the TUI owns input dispatch; the Go InteractiveMode owns that production boundary. Each send traverses the real pre-listener and focused-component path.
func TestUpstreamTerminalColorsQuery(t *testing.T) {
	white, black := tui.RgbColor{R: 255, G: 255, B: 255}, tui.RgbColor{}
	var paletteReplies []string
	for index := range 16 {
		paletteReplies = append(paletteReplies, fmt.Sprintf("\x1b]4;%d;#%02x%02x%02x\x07", index, index, index, index))
	}
	for _, tc := range []struct {
		name       string
		line       int
		replies    []string
		background *tui.RgbColor
		foreground *tui.RgbColor
		palette    bool
		ordinary   bool
		late       bool
	}{
		// terminal-colors.test.ts:137 queries all colors in one write and consumes the replies.
		{name: "queries all colors in one write and consumes the replies", line: 137, replies: append([]string{"\x1b]10;#000000\x07", "\x1b]11;#ffffff\x07"}, paletteReplies...), foreground: &black, background: &white, palette: true},
		// terminal-colors.test.ts:161 resolves on DA1 with the replies that arrived, while ordinary input still dispatches.
		{name: "resolves on DA1 and dispatches non-matching input normally", line: 161, replies: []string{"\x1b]11;#ffffff\x07", "\x1b[?62c"}, background: &white, ordinary: true},
		// terminal-colors.test.ts:179 reports late replies after a timeout and consumes them until DA1.
		{name: "keeps consuming late replies after the timeout", line: 179, replies: []string{"\x1b]11;#ffffff\x07", "\x1b[?62c"}, late: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				out := &bytes.Buffer{}
				renderer := tui.NewWithOutput(out, 80, 24)
				renderer.SetRenderDispatcher(func(func()) {})
				defer renderer.Stop()
				component := &focusedInputProbe{}
				renderer.Add(component)
				renderer.SetFocus(component)
				mode := &InteractiveMode{tuiInst: renderer}
				var listenerInputs []string
				(&ExtUIContext{m: mode}).OnTerminalInput(func(data string) extension.TerminalInputResult {
					listenerInputs = append(listenerInputs, data)
					return extension.TerminalInputResult{}
				})
				timeoutMs := float64(1000)
				if tc.late {
					timeoutMs = 1
				}
				var lateColors []tui.TerminalColors
				query := renderer.QueryTerminalColors(tui.TerminalColorQueryOptions{TimeoutMs: timeoutMs, OnLateReply: func(colors tui.TerminalColors) { lateColors = append(lateColors, colors) }})
				if !strings.HasPrefix(out.String(), "\x1b]10;?\x07\x1b]11;?\x07\x1b]4;0;?\x07") || !strings.HasSuffix(out.String(), "\x1b[c") {
					t.Fatalf("query write = %q", out.String())
				}
				var expectedInputs []string
				if tc.ordinary {
					if err := mode.dispatchKey(t.Context(), "x"); err != nil {
						t.Fatal(err)
					}
					synctest.Wait()
					select {
					case result := <-query:
						t.Fatalf("ordinary input settled query: %+v", result)
					default:
					}
					expectedInputs = []string{"x"}
					if !slices.Equal(listenerInputs, expectedInputs) || !slices.Equal(component.inputs, expectedInputs) {
						t.Fatalf("ordinary input listener=%q focus=%q", listenerInputs, component.inputs)
					}
				}
				if tc.late {
					time.Sleep(5 * time.Millisecond)
					result := <-query
					if result.Colors.Background != nil || result.Err != nil {
						t.Fatalf("timeout=%+v", result)
					}
				}
				for _, reply := range tc.replies {
					if err := mode.dispatchKey(t.Context(), reply); err != nil {
						t.Fatal(err)
					}
				}
				if tc.late {
					if len(lateColors) != 1 || !reflect.DeepEqual(lateColors[0].Background, &white) {
						t.Errorf("upstream line%d late replies = %+v, want the white background once", tc.line, lateColors)
					}
				} else {
					result := <-query
					if result.Err != nil || !reflect.DeepEqual(result.Colors.Background, tc.background) || !reflect.DeepEqual(result.Colors.Foreground, tc.foreground) || (result.Colors.Palette != nil) != tc.palette {
						t.Errorf("upstream line%d colors=%+v err=%v, want background=%v foreground=%v palette=%v", tc.line, result.Colors, result.Err, tc.background, tc.foreground, tc.palette)
					}
				}
				if !slices.Equal(listenerInputs, expectedInputs) || !slices.Equal(component.inputs, expectedInputs) {
					t.Fatalf("color reply leaked: listener=%q focus=%q", listenerInputs, component.inputs)
				}
			})
		})
	}
}
