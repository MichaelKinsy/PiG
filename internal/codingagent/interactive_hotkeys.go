package codingagent

// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts (handleHotkeysCommand).

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/MichaelKinsy/PiG/tui"
)

func hotkeysMarkdown() string {
	var b strings.Builder
	section := func(title string) { fmt.Fprintf(&b, "\n**%s**\n| Key | Action |\n|-----|--------|\n", title) }
	row := func(description string, actions ...string) {
		keys := make([]string, len(actions))
		for i, action := range actions {
			keys[i] = "`" + tui.ActionKeyDisplayText(action) + "`"
		}
		fmt.Fprintf(&b, "| %s | %s |\n", strings.Join(keys, " / "), description)
	}
	section("Navigation")
	row("Move cursor / browse history", "tui.editor.cursorUp", "tui.editor.cursorDown", "tui.editor.cursorLeft", "tui.editor.cursorRight")
	row("Move by word", "tui.editor.cursorWordLeft", "tui.editor.cursorWordRight")
	row("Start of line", "tui.editor.cursorLineStart")
	row("End of line", "tui.editor.cursorLineEnd")
	row("Jump forward to character", "tui.editor.jumpForward")
	row("Jump backward to character", "tui.editor.jumpBackward")
	row("Scroll by page", "tui.editor.pageUp", "tui.editor.pageDown")
	section("Editing")
	row("Send message", "tui.input.submit")
	newLine := "New line"
	if runtime.GOOS == "windows" {
		newLine += " (Ctrl+Enter on Windows Terminal)"
	}
	row(newLine, "tui.input.newLine")
	row("Delete word backwards", "tui.editor.deleteWordBackward")
	row("Delete word forwards", "tui.editor.deleteWordForward")
	row("Delete to start of line", "tui.editor.deleteToLineStart")
	row("Delete to end of line", "tui.editor.deleteToLineEnd")
	row("Paste the most-recently-deleted text", "tui.editor.yank")
	row("Cycle through the deleted text after pasting", "tui.editor.yankPop")
	row("Undo", "tui.editor.undo")
	section("Other")
	row("Path completion / accept autocomplete", "tui.input.tab")
	row("Cancel autocomplete / abort streaming", "app.interrupt")
	row("Clear editor (first) / exit (second)", "app.clear")
	row("Exit (when editor is empty)", "app.exit")
	row("Suspend to background", "app.suspend")
	row("Cycle thinking level", "app.thinking.cycle")
	row("Cycle models", "app.model.cycleForward", "app.model.cycleBackward")
	row("Open model selector", "app.model.select")
	row("Toggle tool output expansion", "app.tools.expand")
	row("Toggle thinking block visibility", "app.thinking.toggle")
	row("Edit message in external editor", "app.editor.external")
	row("Copy selection or last assistant message", "app.message.copy")
	row("Queue follow-up message", "app.message.followUp")
	row("Restore queued messages", "app.message.dequeue")
	row("Paste image or text from clipboard", "app.clipboard.pasteImage")
	b.WriteString("| `/` | Slash commands |\n| `!` | Run bash command |\n| `!!` | Run bash command (excluded from context) |\n")
	return strings.TrimSpace(b.String())
}

func (m *InteractiveMode) handleHotkeysCommand() {
	body := tui.NewPaddedBox(1, 1, nil)
	body.AddChild(tui.NewMarkdown(hotkeysMarkdown()))
	m.appendChatBlock(tui.NewContainer(
		tui.NewDynamicBorder(""),
		tui.NewPaddedText("\x1b[1m"+tui.ActiveTheme().FgText("accent", "Keyboard Shortcuts")+"\x1b[22m", 1, 0, nil),
		tui.NewSpacer(1),
		body,
		tui.NewDynamicBorder(""),
	))
}
