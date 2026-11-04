package mcpext_test

// pi: packages/coding-agent/src/extensions/mcp/ui.ts:61 draws the title with theme.bold, a chalk call.

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/mcpext/mcptui"
	"github.com/MichaelKinsy/PiG/tui"
)

func TestManagerTitleBoldFollowsChalkLevel(t *testing.T) {
	t.Cleanup(func() { tui.SetChalkModifiers(true) })
	render := func() string {
		view := mcptui.NewMcpManagerView(&renderCounter{}, coloredTheme(t), tui.GetTUIKeybindings())
		return strings.Join(view.Render(60), "\n")
	}
	tui.SetChalkModifiers(true)
	if on := render(); !strings.Contains(on, "\x1b[1mMCP servers\x1b[22m") {
		t.Fatalf("chalk on: title is not bold:\n%q", on)
	}
	tui.SetChalkModifiers(false)
	off := render()
	if strings.Contains(off, "\x1b[1m") || !strings.Contains(off, "MCP servers") {
		t.Fatalf("chalk off: title must be plain text with the accent color:\n%q", off)
	}
}
