package codingagent

import (
	"bytes"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// Pi's TUI owns input dispatch; the Go InteractiveMode owns that production boundary. Each send traverses the real pre-listener and focused-component path.
func TestUpstreamTerminalBackgroundColorQuery(t *testing.T) {
	for _, tc := range []struct {
		name     string
		line     int
		reply    string
		color    *tui.RgbColor
		ordinary bool
		late     bool
	}{
		// .upstream/v0.87.1/packages/tui/test/terminal-colors.test.ts:127.
		{"writes OSC 11 query and resolves with the parsed RGB reply", 127, "\x1b]11;#ffffff\x07", &tui.RgbColor{R: 255, G: 255, B: 255}, false, false},
		// .upstream/v0.87.1/packages/tui/test/terminal-colors.test.ts:143.
		{"consumes OSC 11 replies before input listeners and focused component dispatch", 143, "\x1b]11;#000000\x07", &tui.RgbColor{}, false, false},
		// .upstream/v0.87.1/packages/tui/test/terminal-colors.test.ts:168.
		{"consumes unparseable strict OSC 11 replies and resolves undefined", 168, "\x1b]11;not-a-color\x07", nil, false, false},
		// .upstream/v0.87.1/packages/tui/test/terminal-colors.test.ts:193.
		{"dispatches non-matching input normally while waiting for an OSC 11 reply", 193, "\x1b]11;#ffffff\x07", &tui.RgbColor{R: 255, G: 255, B: 255}, true, false},
		// .upstream/v0.87.1/packages/tui/test/terminal-colors.test.ts:226.
		{"keeps consuming a late OSC 11 reply after timeout", 226, "\x1b]11;#ffffff\x07", nil, false, true},
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
				query := renderer.QueryTerminalBackgroundColor(tui.TerminalColorQueryOptions{TimeoutMs: timeoutMs})
				if !strings.Contains(out.String(), "\x1b]11;?\x07") {
					t.Fatal("missing OSC 11 query")
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
					if result.Color != nil || result.Err != nil {
						t.Fatalf("timeout=%+v", result)
					}
				}
				if err := mode.dispatchKey(t.Context(), tc.reply); err != nil {
					t.Fatal(err)
				}
				if !tc.late {
					result := <-query
					if result.Err != nil || !reflect.DeepEqual(result.Color, tc.color) {
						t.Errorf("upstream line%d color=%v err=%v, want %v", tc.line, result.Color, result.Err, tc.color)
					}
				}
				if !slices.Equal(listenerInputs, expectedInputs) || !slices.Equal(component.inputs, expectedInputs) {
					t.Fatalf("OSC11 leaked: listener=%q focus=%q", listenerInputs, component.inputs)
				}
			})
		})
	}
}
