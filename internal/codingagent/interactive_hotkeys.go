package codingagent

// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts (handleHotkeysCommand).

import (
	"cmp"
	"fmt"
	"runtime"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
	"github.com/MichaelKinsy/PiG/tui"
)

// hotkeysMarkdown is the /hotkeys tables (interactive-mode.ts:6809-6919). shortcuts are the extension-registered shortcuts, listed in
// an "Extensions" table, in the order given, by their capitalized key text and their description or, without one, the extension path.
func hotkeysMarkdown(shortcuts []extension.ExtensionShortcut) string {
	var b strings.Builder
	section := func(title string) { fmt.Fprintf(&b, "\n**%s**\n| Key | Action |\n|-----|--------|\n", title) }
	row := func(description string, actions ...string) {
		// pig additive (D92): an action whose built-in the process strips has no key and no row.
		if slices.ContainsFunc(actions, appActionStripped) {
			return
		}
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
	row("Paste files on macOS, images, or text from clipboard", "app.clipboard.pasteImage")
	b.WriteString("| `/` | Slash commands |\n")
	// pig additive (D92): `!` and `!!` run the user bash the bash tool owns.
	if !pigstrip.Has(pigstrip.ListTools, "bash") {
		b.WriteString("| `!` | Run bash command |\n| `!!` | Run bash command (excluded from context) |\n")
	}
	if len(shortcuts) > 0 {
		section("Extensions")
		for _, shortcut := range shortcuts {
			description := shortcut.Description
			if description == "" {
				description = shortcut.ExtensionPath
			}
			fmt.Fprintf(&b, "| `%s` | %s |\n", tui.KeyDisplayText(string(shortcut.Shortcut)), description)
		}
	}
	return strings.TrimSpace(b.String())
}

// extensionShortcutsForHotkeys lists the runner's shortcuts for /hotkeys. The runner keeps them in a map, so the order here is by
// extension path and then key rather than Pi's registration order.
func (m *InteractiveMode) extensionShortcutsForHotkeys() []extension.ExtensionShortcut {
	if m.newRunner == nil || m.keybindings == nil {
		return nil
	}
	registered := m.newRunner.Shortcuts(m.keybindings.GetResolvedBindings())
	shortcuts := make([]extension.ExtensionShortcut, 0, len(registered))
	for key, shortcut := range registered {
		shortcut.Shortcut = key
		shortcuts = append(shortcuts, shortcut)
	}
	slices.SortFunc(shortcuts, func(a, b extension.ExtensionShortcut) int {
		return cmp.Or(strings.Compare(a.ExtensionPath, b.ExtensionPath), strings.Compare(string(a.Shortcut), string(b.Shortcut)))
	})
	return shortcuts
}

func (m *InteractiveMode) handleHotkeysCommand() {
	body := tui.NewPaddedBox(1, 1, nil)
	body.AddChild(tui.NewMarkdownWithOptions(hotkeysMarkdown(m.extensionShortcutsForHotkeys()), 0, 0, m.markdownThemeWithSettings(), nil, nil))
	m.appendChatBlock(tui.NewContainer(
		tui.NewDynamicBorder(),
		tui.NewPaddedText("\x1b[1m"+tui.ActiveTheme().Fg("accent", "Keyboard Shortcuts")+"\x1b[22m", 1, 0, nil),
		tui.NewSpacer(1),
		body,
		tui.NewDynamicBorder(),
	))
}
