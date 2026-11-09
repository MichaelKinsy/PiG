package codingagent

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// /hotkeys and a slash command's Append add a Markdown to the chat that is whole when it is built and stays in the
// transcript. They finish it, so it keeps no memo for a next streamed delta; the rows must be what they were with the
// memos. The hash covers both blocks at several widths and themes, and was taken on the tree before the change.
func TestWholeMarkdownBlocksRenderBytes(t *testing.T) {
	previous := tui.GetTUIKeybindings()
	DefaultKeybindingsManager().syncToTUI()
	prevTheme := tui.ActiveTheme()
	t.Cleanup(func() { tui.SetKeybindings(previous); tui.SetTheme(prevTheme.Name) })
	// The hash was taken on a terminal that is not truecolor; detection reports truecolor for a Windows host or COLORTERM=truecolor.
	previousCaps := tui.GetCapabilities()
	t.Cleanup(func() { tui.SetCapabilities(previousCaps) })
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
	const wantAppended = "0f08cbaba866abe3dd27bed5695bc18ab62de0177df41aec2c7ca4994c75ea48"
	if got := hex.EncodeToString(appended.Sum(nil)); got != wantAppended {
		t.Errorf("appended rows hash %s, want %s", got, wantAppended)
	}
	// The /hotkeys text is the host's: upstream's handleHotkeysCommand adds " (Ctrl+Enter on Windows Terminal)" to a row on win32, Windows
	// keybindings change the keys, and macOS has its own default keys and shows alt as "option" (tui.FormatKeyText), so its rows
	// have one hash per keybinding platform. Linux's was taken on Linux and macOS's on a macos-15 runner (the nightly's race job, which is the
	// only run of this package on macOS); Windows and WSL are not hashed.
	hotkeysRowsHash := map[tui.KeybindingPlatform]string{
		tui.KeybindingPlatformLinux:  "6c6e446dd091ddae67cc390a41ffa268a9c7418088c7a9c5274abd08eb345e76",
		tui.KeybindingPlatformDarwin: "ab2af2a15656f36e688c7975b31fdf80fd6c781d76ab4bf3b96223340b4cee88",
	}
	want, hashed := hotkeysRowsHash[tui.HostKeybindingPlatform()]
	if !hashed {
		return
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != want {
		t.Errorf("rows hash %s, want %s", got, want)
	}
}
