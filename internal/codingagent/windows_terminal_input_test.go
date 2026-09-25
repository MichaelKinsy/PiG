package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// Ground truth captured from a real Windows Terminal (Windows 11 24H2, build
// 26100) with keycap.exe, decoding the win32-input-mode envelopes it emits when
// an application requests DECSET 9001.
//
// The capture settles what Windows actually sends. Windows Terminal answers
// pig's Kitty query (extendedKeyInit: "\x1b[?2004h\x1b[>7u\x1b[?u\x1b[c") with
// "\x1b[?7u": it enables the same Kitty keyboard protocol flags Ghostty does
// (1+2+4: disambiguate + report-events + report-alternate) and it reports key
// releases. Windows is therefore not a separate input protocol needing its own
// decoder: it is a Kitty terminal, and the Kitty handling is what must be
// correct there.
//
// win32-input-mode records ("\x1b[Vk;Sc;Uc;Kd;Cs;Rc_") appear only while an
// application holds DECSET 9001 open. pig never enables it (asserted by
// TestExtendedKeyInitDoesNotEnableWin32InputMode in tui), so those
// records cannot reach pig's decoders.
//
// These encodings are measured, not derived from the spec. That distinction
// matters: every input bug this session came from a decoder tested against the
// byte forms its author assumed rather than the forms a terminal emits.
// setWindowsTerminalSession pins the variables upstream's
// isWindowsTerminalSession reads, so the host terminal cannot change a case.
func setWindowsTerminalSession(t *testing.T, wtSession, sshConnection string) {
	t.Helper()
	t.Setenv("WT_SESSION", wtSession)
	t.Setenv("SSH_CONNECTION", sshConnection)
	t.Setenv("SSH_CLIENT", "")
	t.Setenv("SSH_TTY", "")
}

func TestWindowsTerminalMeasuredEncodings(t *testing.T) {
	t.Run("keys decoder B owns", func(t *testing.T) {
		tests := []struct {
			name string
			in   string
			want keyAction
		}{
			// Windows Terminal reported Enter as a native record with
			// UnicodeChar 13, i.e. a bare "\r" once the win32 envelope is gone.
			{"enter", "\r", actionSubmit},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				if got := classifyKey(tc.in); got != tc.want {
					t.Errorf("classifyKey(%q) = %d, want %d", tc.in, got, tc.want)
				}
			})
		}
	})

	t.Run("keys decoder A owns", func(t *testing.T) {
		setWindowsTerminalSession(t, "", "")
		tests := []struct {
			name  string
			in    string
			keyID string
		}{
			// Arrows arrive as Kitty CSI-u with an explicit event type.
			{"up press", "\x1b[1;1:1A", "up"},
			// Modified keys arrive as Kitty CSI-u keyed by code point.
			{"ctrl+v press", "\x1b[118;5:1u", "ctrl+v"},
			{"ctrl+backspace press", "\x1b[127;5:1u", "ctrl+backspace"},
			// Outside a local Windows Terminal session a bare "\x08" is
			// plain backspace (keys.ts:1287).
			{"backspace", "\x08", "backspace"},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				if !tui.MatchesKeyID(tc.in, tc.keyID) {
					t.Errorf("MatchesKeyID(%q, %q) = false; Windows Terminal emits this form", tc.in, tc.keyID)
				}
			})
		}
	})

	// The capture's UnicodeChar 8 for Backspace is the console input record,
	// which reaches pig only under win32-input-mode. Without it Windows
	// Terminal sends DEL for Backspace and a bare "\x08" for Ctrl+Backspace,
	// so upstream reads "\x08" as ctrl+backspace in a local Windows Terminal
	// session and as backspace over SSH or elsewhere (keys.ts:1287,
	// isWindowsTerminalSession).
	t.Run("raw backspace bytes follow the Windows Terminal session", func(t *testing.T) {
		tests := []struct {
			name      string
			wtSession string
			ssh       string
			in        string
			keyID     string
			notKeyID  string
		}{
			{"local session BS", "wt-session", "", "\x08", "ctrl+backspace", "backspace"},
			{"local session DEL", "wt-session", "", "\x7f", "backspace", "ctrl+backspace"},
			{"session over SSH BS", "wt-session", "10.0.0.1 50000 10.0.0.2 22", "\x08", "backspace", "ctrl+backspace"},
			{"no session BS", "", "", "\x08", "backspace", "ctrl+backspace"},
			{"no session DEL", "", "", "\x7f", "backspace", "ctrl+backspace"},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				setWindowsTerminalSession(t, tc.wtSession, tc.ssh)
				if !tui.MatchesKeyID(tc.in, tc.keyID) {
					t.Errorf("MatchesKeyID(%q, %q) = false with WT_SESSION=%q SSH_CONNECTION=%q", tc.in, tc.keyID, tc.wtSession, tc.ssh)
				}
				if tui.MatchesKeyID(tc.in, tc.notKeyID) {
					t.Errorf("MatchesKeyID(%q, %q) = true with WT_SESSION=%q SSH_CONNECTION=%q", tc.in, tc.notKeyID, tc.wtSession, tc.ssh)
				}
			})
		}
	})

	t.Run("releases are recognized", func(t *testing.T) {
		// Windows Terminal sends a release for every press, exactly as Ghostty
		// does, so anything that dispatches raw chunks double-fires without
		// this check.
		for _, enc := range []string{
			"\x1b[1;1:3A",   // up release
			"\x1b[127;5:3u", // ctrl+backspace release
		} {
			if !tui.IsKeyRelease(enc) {
				t.Errorf("IsKeyRelease(%q) = false; Windows Terminal emits this release form", enc)
			}
		}
	})

	t.Run("press events are not mistaken for releases", func(t *testing.T) {
		for _, enc := range []string{"\x1b[1;1:1A", "\x1b[118;5:1u", "\x08", "\r"} {
			if tui.IsKeyRelease(enc) {
				t.Errorf("IsKeyRelease(%q) = true; a press would be swallowed", enc)
			}
		}
	})
}
