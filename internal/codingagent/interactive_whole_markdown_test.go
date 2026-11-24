//go:build !pig_strip_syntax_highlight

package codingagent

import (
	"crypto/sha256"
	"encoding/hex"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// /hotkeys and a slash command's Append add a Markdown to the chat that is whole when it is built and stays in the
// transcript. They finish it, so it keeps no memo for a next streamed delta; the rows must be what they were with the
// memos. The hash covers both blocks at several widths and themes, and was taken on the tree before the change.
// It was taken again on the tree that carries Pi 562da75f9, which sets default: (s) => t.fg("text", s) in
// buildCliHighlightTheme (packages/coding-agent/src/modes/interactive/theme/theme.ts:894), wrapping unscoped code tokens
// in the theme text foreground escape.
func TestWholeMarkdownBlocksRenderBytes(t *testing.T) {
	restoreStartupTheme(t)
	previous := tui.GetTUIKeybindings()
	DefaultKeybindingsManager().syncToTUI()
	t.Cleanup(func() { tui.SetKeybindings(previous) })
	// The hash was taken on a terminal that is not truecolor; detection reports truecolor for a Windows host or COLORTERM=truecolor.
	tui.SetCapabilities(tui.TerminalCapabilities{})
	text := "# Heading\n\nSome **bold**, *italic* and `code` with a [link](https://example.com).\n\n- one\n- two\n\n```go\nfmt.Println(1)\n```\n\n" + strings.Repeat("A long line that wraps at every width tested. ", 6) + "\n"
	hash, appended := sha256.New(), sha256.New()
	for _, theme := range []string{"dark", "light"} {
		tui.SetTheme(theme)
		for _, width := range []int{20, 40, 80, 120} {
			m := &InteractiveMode{chatContainer: tui.NewContainer()}
			m.handleHotkeysCommand()
			sc := m.buildSlashContext(t.Context())
			sc.Append(text)
			only := &InteractiveMode{chatContainer: tui.NewContainer()}
			only.buildSlashContext(t.Context()).Append(text)
			for range 2 {
				hash.Write([]byte(strings.Join(m.chatContainer.Render(width), "\n")))
				hash.Write([]byte{0})
				m.chatContainer.Invalidate()
				appended.Write([]byte(strings.Join(only.chatContainer.Render(width), "\n")))
				appended.Write([]byte{0})
				only.chatContainer.Invalidate()
			}
		}
	}
	const wantAppended = "66447a6926008181da7a5892d125fc76ab18b7b8c817f4c4c4a553688ba6227e"
	if got := hex.EncodeToString(appended.Sum(nil)); got != wantAppended {
		t.Errorf("appended rows hash %s, want %s", got, wantAppended)
	}
	// The /hotkeys text is the host's: upstream's handleHotkeysCommand adds " (Ctrl+Enter on Windows Terminal)" to a row on win32 only, and Windows
	// keybindings (native Windows and WSL) change the keys, so its rows have one hash on Windows (not taken here), one on WSL, with the Windows keys and no
	// Ctrl+Enter row, one on macOS, where keys display Alt as Option (formatKeyText, keybinding-hints.ts), and one elsewhere; the Linux one was taken on Linux
	// and the WSL one on Linux with WSL_DISTRO_NAME set. The macOS one is what the macOS runner printed; it is also what Linux computes with the Option
	// display forced.
	if runtime.GOOS == "windows" {
		return
	}
	want := "d9c6e895cd305624413664c65a05c5047fbdd78e855091255b65ebc6fb3e47f8"
	switch {
	case tui.HostKeybindingPlatform() == tui.KeybindingPlatformLinuxWSL:
		want = "4c4149c4b245033e7f8446c0d683ee902e7528c3d75315f52d00d4015faed4fc"
	case runtime.GOOS == "darwin":
		want = "ab2af2a15656f36e688c7975b31fdf80fd6c781d76ab4bf3b96223340b4cee88"
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != want {
		t.Errorf("rows hash %s, want %s", got, want)
	}
}

// These tests select a theme, so TestThemeTestsRestoreTheProcessTheme runs them too; a build that strips what they need does not link them, and neither does it have the entries.
func init() {
	themeRestoringTests["TestWholeMarkdownBlocksRenderBytes"] = TestWholeMarkdownBlocksRenderBytes
}
