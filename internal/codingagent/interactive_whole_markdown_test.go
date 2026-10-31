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
	text := "# Heading\n\nSome **bold**, *italic* and `code` with a [link](https://example.com).\n\n- one\n- two\n\n```go\nfmt.Println(1)\n```\n\n" + strings.Repeat("A long line that wraps at every width tested. ", 6) + "\n"
	hash := sha256.New()
	for _, theme := range []string{"dark", "light"} {
		tui.SetTheme(theme)
		for _, width := range []int{20, 40, 80, 120} {
			m := &InteractiveMode{chatContainer: tui.NewContainer()}
			m.handleHotkeysCommand()
			sc := m.buildSlashContext(t.Context())
			sc.Append(text)
			for range 2 {
				hash.Write([]byte(strings.Join(m.chatContainer.Render(width), "\n")))
				hash.Write([]byte{0})
				m.chatContainer.Invalidate()
			}
		}
	}
	const want = "d8e55b37859313bb4b3fed37d406a2520cb5e3033854454dc951c99875ea2176"
	if got := hex.EncodeToString(hash.Sum(nil)); got != want {
		t.Errorf("rows hash %s, want %s", got, want)
	}
}
