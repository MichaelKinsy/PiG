//go:build linux

package codingagent_test

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// startModeTokens matches the mode writes whose order Pi's start() fixes: the alternate screen's enter, bracketed paste on, the Kitty keyboard push, its
// query and pop, the Device Attributes query, and the modifyOtherKeys fallback.
var startModeTokens = regexp.MustCompile(`\x1b\[\?1049h|\x1b\[\?2004h|\x1b\[>7u|\x1b\[<u|\x1b\[\?u|\x1b\[c|\x1b\[>4;2m`)

// Pi's start (tui.ts:956) writes, up to its first frame: beforeTerminalStart (a fullscreen start enters the alternate screen: tui-alt-screen.ts:347), then
// terminal.start (terminal.ts:207): bracketed paste on (:220), the Kitty keyboard push and query with the Device Attributes sentinel (:237, queryAndEnableKittyProtocol).
// `CSI > 4 ; 2 m` (modifyOtherKeys) is written only when a reply asks for it (:417: a Kitty reply with zero flags, or the sentinel answered with no Kitty reply), so after
// the sentinel and never when the terminal answers with Kitty flags or not at all. The Kitty flags are pushed once, on the screen the renderer draws on: no pop and
// no second push come before the first frame.
//
// The child runs a real mode on a pseudo-terminal that the test answers the way each kind of terminal does: one that supports the Kitty keyboard protocol, one that
// answers only Device Attributes (a plain xterm-like terminal), and one that answers nothing.
func TestStartModeWritesFollowPiStartOrder(t *testing.T) {
	const da1Reply = "\x1b[?62;4;52c"
	terminals := []struct {
		name     string
		reply    string
		modifies bool // `CSI > 4 ; 2 m` is written
	}{
		{"kitty terminal", "\x1b[?7u" + da1Reply, false},
		{"device attributes only", da1Reply, true},
		{"no reply", "", false},
	}
	for _, tuiMode := range []string{"fullscreen", "regular"} {
		want := []string{"\x1b[?2004h", "\x1b[>7u", "\x1b[?u", "\x1b[c"}
		if tuiMode == "fullscreen" {
			want = append([]string{"\x1b[?1049h"}, want...)
		}
		for _, terminal := range terminals {
			t.Run(fmt.Sprintf("%s %s", tuiMode, terminal.name), func(t *testing.T) {
				child := startExitBytesChild(t, false, false, exitBytesTuiModeEnv+"="+tuiMode)
				child.await("the Device Attributes query", func() bool { return strings.Contains(child.captured.from(0), "\x1b[c") })
				if terminal.reply != "" {
					child.write(terminal.reply)
				}
				child.await("the mode to be ready for input", child.ready)
				if terminal.modifies {
					// The reply is read by the input loop, so the fallback lands before or after the first frame depending on timing, in Pi as here.
					child.await("the modifyOtherKeys fallback", func() bool { return strings.Contains(child.captured.from(0), "\x1b[>4;2m") })
				}
				child.write("\x04")
				child.finish()
				all := child.captured.from(0)
				written := all
				if frame := strings.Index(written, "\x1b[?2026h"); frame >= 0 {
					written = written[:frame]
				}
				got := startModeTokens.FindAllString(written, -1)
				// The first Device Attributes query ends the start; the colour query's follows it, and the reply to the first is written between or after.
				first := slices.Index(got, "\x1b[c")
				if first < 0 || !slices.Equal(got[:first+1], want) {
					t.Fatalf("%s start wrote %q, want Pi's %q first", tuiMode, got, want)
				}
				if count := strings.Count(written, "\x1b[<u"); count != 0 {
					t.Errorf("a keyboard pop before the first frame: %q", got)
				}
				// The drain at the exit writes the pop (terminal.ts:449 through drainInput) and stop() finds the flags gone, so one `CSI < u` is the whole output's.
				if count := strings.Count(all, "\x1b[<u"); count != 1 {
					t.Errorf("%d keyboard pops in the whole output, want 1", count)
				}
				if count := strings.Count(written, "\x1b[>7u"); count != 1 {
					t.Errorf("%d keyboard pushes before the first frame, want 1: %q", count, got)
				}
				modify := strings.Index(all, "\x1b[>4;2m")
				switch {
				case terminal.modifies && (modify < strings.Index(all, "\x1b]11;?") || strings.Count(all, "\x1b[>4;2m") != 1):
					// Pi reads the reply only after start() and init()'s colour query ran (tui.ts:956, interactive-mode.ts init()), so the fallback follows those writes.
					t.Errorf("modifyOtherKeys: want one write after the sentinel and the colour query, got %q", startModeTokens.FindAllString(all, -1))
				case !terminal.modifies && modify >= 0:
					t.Errorf("modifyOtherKeys written without a reply that asks for it: %q", startModeTokens.FindAllString(all, -1))
				}
			})
		}
	}
}
