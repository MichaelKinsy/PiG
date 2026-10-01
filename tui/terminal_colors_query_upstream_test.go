package tui

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// .upstream/v0.99.2/packages/tui/test/terminal-colors.test.ts:106.
func TestUpstreamParseOscColorResponse(t *testing.T) {
	// terminal-colors.test.ts:107.
	t.Run("parses OSC 10, 11, and 4 replies", func(t *testing.T) {
		for _, tc := range []struct {
			input string
			want  OscColorReply
		}{
			{"\x1b]10;rgb:ffff/ffff/ffff\x07", OscColorReply{Target: OscColorTargetForeground, RGB: &RgbColor{R: 255, G: 255, B: 255}}},
			{"\x1b]4;13;#ff0080\x1b\\", OscColorReply{Target: 13, RGB: &RgbColor{R: 255, G: 0, B: 128}}},
			{"\x1b]4;1;bogus\x07", OscColorReply{Target: 1}},
			{"\x1b]11;#ffffff\x07", OscColorReply{Target: OscColorTargetBackground, RGB: &RgbColor{R: 255, G: 255, B: 255}}},
		} {
			got, ok := ParseOscColorResponse(tc.input)
			if !ok || !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ParseOscColorResponse(%q) = %+v, %v; want %+v", tc.input, got, ok, tc.want)
			}
		}
		if got, ok := ParseOscColorResponse("\x1b]12;#ffffff\x07"); ok {
			t.Errorf("OSC 12 parsed as %+v", got)
		}
	})
}

type recordedWrites struct{ writes []string }

func (w *recordedWrites) Write(data []byte) (int, error) {
	w.writes = append(w.writes, string(data))
	return len(data), nil
}

func (w *recordedWrites) last() string {
	if len(w.writes) == 0 {
		return ""
	}
	return w.writes[len(w.writes)-1]
}

var (
	paletteReplies = func() []string {
		replies := make([]string, 16)
		for i := range replies {
			replies[i] = "\x1b]4;" + strconv.Itoa(i) + ";#000000\x07"
		}
		return replies
	}()
	blackColor = RgbColor{}
	whiteColor = RgbColor{R: 255, G: 255, B: 255}
)

const deviceAttributesReply = "\x1b[?62;22c"

func blackPalette() []RgbColor { return make([]RgbColor, 16) }

// .upstream/v0.99.2/packages/tui/test/terminal-colors.test.ts:136. The Go renderer leaves input dispatch to its driver, so these cases exercise the FIFO and consumption seam; the InteractiveMode boundary is covered in internal/codingagent.
func TestUpstreamQueryTerminalColors(t *testing.T) {
	// terminal-colors.test.ts:137.
	t.Run("queries all colors in one write and consumes the replies", func(t *testing.T) {
		out := &recordedWrites{}
		ui := NewWithOutput(out, 80, 24)
		query := ui.QueryTerminalColors(TerminalColorQueryOptions{TimeoutMs: 1000})
		written := out.last()
		if !strings.HasPrefix(written, "\x1b]10;?\x07\x1b]11;?\x07\x1b]4;0;?\x07") || !strings.HasSuffix(written, "\x1b[c") {
			t.Fatalf("query write = %q", written)
		}
		if ui.ConsumeTerminalColorResponse("x") {
			t.Fatal("ordinary input was consumed")
		}
		for _, reply := range append([]string{"\x1b]10;#ffffff\x07", "\x1b]11;rgb:0000/0000/0000\x1b\\"}, paletteReplies...) {
			if !ui.ConsumeTerminalColorResponse(reply) {
				t.Fatalf("reply %q was not consumed", reply)
			}
		}
		// Resolves once every reply arrived, without waiting for DA1.
		select {
		case got := <-query:
			want := TerminalColors{Foreground: &whiteColor, Background: &blackColor, Palette: blackPalette()}
			if got.Err != nil || !reflect.DeepEqual(got.Colors, want) {
				t.Fatalf("result = %+v, want %+v", got, want)
			}
		default:
			t.Fatal("query did not resolve after every color replied")
		}
		if !ui.ConsumeTerminalColorResponse(deviceAttributesReply) {
			t.Fatal("DA1 reply was not consumed")
		}
	})

	// terminal-colors.test.ts:161.
	t.Run("resolves on DA1 with the replies that arrived, in query order", func(t *testing.T) {
		ui := NewWithOutput(&recordedWrites{}, 80, 24)
		first := ui.QueryTerminalColors(TerminalColorQueryOptions{TimeoutMs: 1000})
		second := ui.QueryTerminalColors(TerminalColorQueryOptions{TimeoutMs: 1000})
		replies := append([]string{"\x1b]11;#000000\x07"}, paletteReplies[:8]...)
		// An incomplete palette is dropped.
		replies = append(replies, deviceAttributesReply, deviceAttributesReply)
		for _, reply := range replies {
			if !ui.ConsumeTerminalColorResponse(reply) {
				t.Fatalf("reply %q was not consumed", reply)
			}
		}
		if got := <-first; got.Err != nil || !reflect.DeepEqual(got.Colors, TerminalColors{Background: &blackColor}) {
			t.Fatalf("first = %+v", got)
		}
		if got := <-second; got.Err != nil || !reflect.DeepEqual(got.Colors, TerminalColors{}) {
			t.Fatalf("second = %+v", got)
		}
	})

	// terminal-colors.test.ts:179.
	t.Run("reports late replies after a timeout and consumes them until DA1", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			ui := NewWithOutput(&recordedWrites{}, 80, 24)
			var late []TerminalColors
			query := ui.QueryTerminalColors(TerminalColorQueryOptions{TimeoutMs: 1, OnLateReply: func(colors TerminalColors) { late = append(late, colors) }})
			time.Sleep(5 * time.Millisecond)
			if got := <-query; got.Err != nil || got.Colors.Background != nil {
				t.Fatalf("timeout result = %+v", got)
			}
			if !ui.ConsumeTerminalColorResponse("\x1b]11;#ffffff\x07") || !ui.ConsumeTerminalColorResponse(deviceAttributesReply) {
				t.Fatal("late replies were not consumed")
			}
			if want := []TerminalColors{{Background: &whiteColor}}; !reflect.DeepEqual(late, want) {
				t.Fatalf("late replies = %+v, want %+v", late, want)
			}
			// With no query pending, color replies are ordinary input again.
			if ui.ConsumeTerminalColorResponse("\x1b]11;#ffffff\x07") {
				t.Fatal("unsolicited color reply was consumed")
			}
		})
	})
}

// tui.ts:1139-1143 (consumeTerminalColorResponse): a target that already replied is consumed but neither overwrites its color nor counts toward the 18 replies that complete the query without DA1.
func TestQueryTerminalColorsIgnoresDuplicateTargets(t *testing.T) {
	ui := NewWithOutput(&recordedWrites{}, 80, 24)
	query := ui.QueryTerminalColors(TerminalColorQueryOptions{TimeoutMs: 1000})
	replies := append([]string{"\x1b]11;#ffffff\x07", "\x1b]11;#000000\x07", "\x1b]10;#ffffff\x07"}, paletteReplies[:15]...)
	for _, reply := range replies {
		if !ui.ConsumeTerminalColorResponse(reply) {
			t.Fatalf("reply %q was not consumed", reply)
		}
	}
	select {
	case got := <-query:
		t.Fatalf("17 distinct targets and a duplicate completed the query: %+v", got)
	default:
	}
	if !ui.ConsumeTerminalColorResponse(paletteReplies[15]) {
		t.Fatal("last palette reply was not consumed")
	}
	select {
	case got := <-query:
		want := TerminalColors{Foreground: &whiteColor, Background: &whiteColor, Palette: blackPalette()}
		if got.Err != nil || !reflect.DeepEqual(got.Colors, want) {
			t.Fatalf("result = %+v, want the first background reply kept: %+v", got, want)
		}
	default:
		t.Fatal("query did not resolve after every target replied")
	}
}
