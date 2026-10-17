package codingagent

import (
	"bytes"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// Pi's TUI owns input dispatch; the Go InteractiveMode owns that production boundary. Each send traverses the real pre-listener and focused-component path. .upstream/v0.99.2/packages/tui/test/terminal-colors.test.ts:136.
func TestUpstreamQueryTerminalColorsInputBoundary(t *testing.T) {
	palette := make([]string, 16)
	for i := range palette {
		palette[i] = "\x1b]4;" + strconv.Itoa(i) + ";#000000\x07"
	}
	const da1 = "\x1b[?62;22c"
	black, white := tui.RgbColor{}, tui.RgbColor{R: 255, G: 255, B: 255}

	type harness struct {
		renderer       tui.TUI
		out            *bytes.Buffer
		mode           *InteractiveMode
		component      *focusedInputProbe
		listenerInputs *[]string
	}
	setup := func(t *testing.T) harness {
		out := &bytes.Buffer{}
		renderer := tui.NewWithOutput(out, 80, 24)
		renderer.SetRenderDispatcher(func(func()) {})
		t.Cleanup(renderer.Stop)
		component := &focusedInputProbe{}
		renderer.Add(component)
		renderer.SetFocus(component)
		mode := &InteractiveMode{tuiInst: renderer}
		listenerInputs := &[]string{}
		(&ExtUIContext{m: mode}).OnTerminalInput(func(data string) extension.TerminalInputResult {
			*listenerInputs = append(*listenerInputs, data)
			return extension.TerminalInputResult{}
		})
		return harness{renderer, out, mode, component, listenerInputs}
	}
	send := func(t *testing.T, h harness, data ...string) {
		t.Helper()
		for _, chunk := range data {
			if err := h.mode.dispatchKey(t.Context(), chunk); err != nil {
				t.Fatal(err)
			}
		}
	}

	// terminal-colors.test.ts:137.
	t.Run("queries all colors in one write and consumes the replies", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			h := setup(t)
			query := h.renderer.QueryTerminalColors(tui.TerminalColorQueryOptions{TimeoutMs: 1000})
			written := h.out.String()
			if !strings.Contains(written, "\x1b]10;?\x07\x1b]11;?\x07\x1b]4;0;?\x07") || !strings.HasSuffix(written, "\x1b[c") {
				t.Fatalf("query write = %q", written)
			}
			send(t, h, "x", "\x1b]10;#ffffff\x07", "\x1b]11;rgb:0000/0000/0000\x1b\\")
			send(t, h, palette...)
			// Resolves once every reply arrived, without waiting for DA1.
			got := <-query
			want := tui.TerminalColors{Foreground: &white, Background: &black, Palette: make([]tui.RgbColor, 16)}
			if got.Err != nil || !reflect.DeepEqual(got.Colors, want) {
				t.Fatalf("result = %+v, want %+v", got, want)
			}
			send(t, h, da1)
			if !slices.Equal(h.component.inputs, []string{"x"}) || !slices.Equal(*h.listenerInputs, []string{"x"}) {
				t.Fatalf("replies leaked: listener=%q focus=%q", *h.listenerInputs, h.component.inputs)
			}
		})
	})

	// terminal-colors.test.ts:161.
	t.Run("resolves on DA1 with the replies that arrived, in query order", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			h := setup(t)
			first := h.renderer.QueryTerminalColors(tui.TerminalColorQueryOptions{TimeoutMs: 1000})
			second := h.renderer.QueryTerminalColors(tui.TerminalColorQueryOptions{TimeoutMs: 1000})
			send(t, h, "\x1b]11;#000000\x07")
			// An incomplete palette is dropped.
			send(t, h, palette[:8]...)
			send(t, h, da1, da1)
			if got := <-first; got.Err != nil || !reflect.DeepEqual(got.Colors, tui.TerminalColors{Background: &black}) {
				t.Fatalf("first = %+v", got)
			}
			if got := <-second; got.Err != nil || !reflect.DeepEqual(got.Colors, tui.TerminalColors{}) {
				t.Fatalf("second = %+v", got)
			}
			if len(h.component.inputs) != 0 || len(*h.listenerInputs) != 0 {
				t.Fatalf("replies leaked: listener=%q focus=%q", *h.listenerInputs, h.component.inputs)
			}
		})
	})

	// terminal-colors.test.ts:179.
	t.Run("reports late replies after a timeout and consumes them until DA1", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			h := setup(t)
			var late []tui.TerminalColors
			query := h.renderer.QueryTerminalColors(tui.TerminalColorQueryOptions{TimeoutMs: 1, OnLateReply: func(colors tui.TerminalColors) { late = append(late, colors) }})
			time.Sleep(5 * time.Millisecond)
			if got := <-query; got.Err != nil || got.Colors.Background != nil {
				t.Fatalf("timeout result = %+v", got)
			}
			send(t, h, "\x1b]11;#ffffff\x07", da1)
			if want := []tui.TerminalColors{{Background: &white}}; !reflect.DeepEqual(late, want) {
				t.Fatalf("late replies = %+v, want %+v", late, want)
			}
			if len(h.component.inputs) != 0 {
				t.Fatalf("late replies leaked to the focused component: %q", h.component.inputs)
			}
			// With no query pending, color replies are ordinary input again.
			send(t, h, "\x1b]11;#ffffff\x07")
			if want := []string{"\x1b]11;#ffffff\x07"}; !slices.Equal(h.component.inputs, want) {
				t.Fatalf("component inputs = %q, want %q", h.component.inputs, want)
			}
		})
	})
}
