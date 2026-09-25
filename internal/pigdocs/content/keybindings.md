# Keybindings

PiG reads keybindings from `~/.pig/agent/keybindings.json`. The file is optional.
If it is absent, PiG uses the defaults in this page.

Each entry maps one action to one or more keys. To change a binding, write the
action name and the keys you want:

```json
{
  "app.tools.expand": ["ctrl+e"],
  "app.session.tree": ["ctrl+shift+t"]
}
```

A binding in the file replaces the default for that action. It does not add to
it. Bindings you do not name keep their defaults. Set an action to `[]` to leave
the action with no key.

Set `PIG_CODING_AGENT_DIR` to read the file from a different directory.

Use `/hotkeys` to list the keys that are active in the current session.

If two actions claim the same key, PiG records the conflict and keeps both
bindings. Both actions then answer that key, so give one of them a different key.

## Defaults

The keys below are the macOS defaults. Platform differences are listed after the
tables.

### Session control

| Action | Default key | Effect |
|---|---|---|
| `app.clear` | `ctrl+c` | Clear editor |
| `app.exit` | `ctrl+d` | Exit when editor is empty |
| `app.interrupt` | `escape` | Cancel or abort |
| `app.session.delete` | `ctrl+d` | Delete session |
| `app.session.deleteNoninvasive` | `ctrl+backspace` | Delete session when query is empty |
| `app.session.fork` | none | Fork current session |
| `app.session.new` | none | Start a new session |
| `app.session.rename` | `ctrl+r` | Rename session |
| `app.session.resume` | none | Resume a session |
| `app.session.toggleNamedFilter` | `ctrl+n` | Toggle named session filter |
| `app.session.togglePath` | `ctrl+p` | Toggle session path display |
| `app.session.toggleSort` | `ctrl+s` | Toggle session sort mode |
| `app.session.tree` | none | Open session tree |
| `app.suspend` | `ctrl+z` | Suspend to background |

### Editing and messages

| Action | Default key | Effect |
|---|---|---|
| `app.clipboard.pasteImage` | `ctrl+v` | Paste image from clipboard (text fallback) |
| `app.editor.external` | `ctrl+g` | Open external editor |
| `app.message.copy` | `ctrl+x` | Copy selection or last assistant message |
| `app.message.dequeue` | `alt+up` | Restore queued messages |
| `app.message.followUp` | `alt+enter` | Queue follow-up message |

### Models

| Action | Default key | Effect |
|---|---|---|
| `app.model.cycleBackward` | `shift+ctrl+p` | Cycle to previous model |
| `app.model.cycleForward` | `ctrl+p` | Cycle to next model |
| `app.model.select` | `ctrl+l` | Open model selector |
| `app.models.clearAll` | `ctrl+x` | Clear all models |
| `app.models.enableAll` | `ctrl+a` | Enable all models |
| `app.models.reorderDown` | `alt+down` | Move model down in order |
| `app.models.reorderUp` | `alt+up` | Move model up in order |
| `app.models.save` | `ctrl+s` | Save model selection |
| `app.models.toggleProvider` | `ctrl+p` | Toggle all models for provider |
| `app.thinking.save` | `ctrl+s` | Save the selected thinking level as the default, in the thinking level selector |

### Display

| Action | Default key | Effect |
|---|---|---|
| `app.thinking.cycle` | `shift+tab` | Cycle thinking level |
| `app.thinking.toggle` | `ctrl+t` | Toggle thinking blocks |
| `app.tools.expand` | `ctrl+o` | Toggle tool details |

### Session tree

| Action | Default key | Effect |
|---|---|---|
| `app.tree.editLabel` | `shift+l` | Edit tree label |
| `app.tree.filter.all` | `ctrl+a` | Tree filter: show all entries |
| `app.tree.filter.cycleBackward` | `shift+ctrl+o` | Tree filter: cycle backward |
| `app.tree.filter.cycleForward` | `ctrl+o` | Tree filter: cycle forward |
| `app.tree.filter.default` | `ctrl+d` | Tree filter: default view |
| `app.tree.filter.labeledOnly` | `ctrl+l` | Tree filter: labeled entries only |
| `app.tree.filter.noTools` | `ctrl+t` | Tree filter: hide tool results |
| `app.tree.filter.userOnly` | `ctrl+u` | Tree filter: user messages only |
| `app.tree.foldOrUp` | `alt+left, ctrl+left` | Fold tree branch or move up |
| `app.tree.toggleLabelTimestamp` | `shift+t` | Toggle tree label timestamps |
| `app.tree.unfoldOrDown` | `alt+right, ctrl+right` | Unfold tree branch or move down |

Several actions share a key, such as `ctrl+s` for `app.session.toggleSort`,
`app.models.save` and `app.thinking.save`. Each one applies only in its own
selector, so they do not conflict.

## Terminal UI actions

Actions named `tui.*` control the editor, lists, and the fullscreen transcript.
Rebind them in `keybindings.json` the same way as `app.*` actions.

### Cursor movement

| Action | Default key | Effect |
|---|---|---|
| `tui.editor.cursorUp` | `up` | Move cursor up, or browse older history at the top |
| `tui.editor.cursorDown` | `down` | Move cursor down, or browse newer history at the bottom |
| `tui.editor.historyPrevious` | none | Select the previous prompt history entry |
| `tui.editor.historyNext` | none | Select the next prompt history entry |
| `tui.editor.cursorLeft` | `left`, `ctrl+b` | Move cursor left |
| `tui.editor.cursorRight` | `right`, `ctrl+f` | Move cursor right |
| `tui.editor.cursorWordLeft` | `alt+left`, `ctrl+left`, `alt+b` | Move cursor one word left |
| `tui.editor.cursorWordRight` | `alt+right`, `ctrl+right`, `alt+f` | Move cursor one word right |
| `tui.editor.cursorLineStart` | `home`, `ctrl+home`, `ctrl+a` | Move to line start |
| `tui.editor.cursorLineEnd` | `end`, `ctrl+end`, `ctrl+e` | Move to line end |
| `tui.editor.jumpForward` | `ctrl+]` | Jump forward to a character |
| `tui.editor.jumpBackward` | `ctrl+alt+]` | Jump backward to a character |
| `tui.editor.pageUp` | `pageUp`, `ctrl+pageUp` | Scroll the editor up one page |
| `tui.editor.pageDown` | `pageDown`, `ctrl+pageDown` | Scroll the editor down one page |

### Text editing

| Action | Default key | Effect |
|---|---|---|
| `tui.editor.deleteCharBackward` | `backspace` | Delete character backward |
| `tui.editor.deleteCharForward` | `delete`, `ctrl+d` | Delete character forward |
| `tui.editor.deleteWordBackward` | `ctrl+w`, `alt+backspace` | Delete word backward |
| `tui.editor.deleteWordForward` | `alt+d`, `alt+delete` | Delete word forward |
| `tui.editor.deleteToLineStart` | `ctrl+u` | Delete to line start |
| `tui.editor.deleteToLineEnd` | `ctrl+k` | Delete to line end |
| `tui.editor.yank` | `ctrl+y` | Paste the most recently deleted text |
| `tui.editor.yankPop` | `alt+y` | Cycle through deleted text after a yank |
| `tui.editor.undo` | `ctrl+-` | Undo the last edit |

### Input and selection

| Action | Default key | Effect |
|---|---|---|
| `tui.input.newLine` | `shift+enter`, `ctrl+j` | Insert a new line |
| `tui.input.submit` | `enter` | Submit input |
| `tui.input.tab` | `tab` | Tab or autocomplete |
| `tui.input.copy` | `ctrl+c` | Copy selection |
| `tui.select.up` | `up` | Move selection up |
| `tui.select.down` | `down` | Move selection down |
| `tui.select.pageUp` | `pageUp` | Page up in a list |
| `tui.select.pageDown` | `pageDown` | Page down in a list |
| `tui.select.confirm` | `enter` | Confirm selection |
| `tui.select.cancel` | `escape`, `ctrl+c` | Cancel selection |

### Fullscreen transcript

These actions apply when `tuiMode` is `fullscreen`. They take precedence over
editor actions that use the same key.

| Action | Default key | Effect |
|---|---|---|
| `tui.altScreen.pageUp` | `pageUp` | Scroll the transcript up one page |
| `tui.altScreen.pageDown` | `pageDown` | Scroll the transcript down one page |
| `tui.altScreen.halfPageUp` | none | Scroll the transcript up half a page |
| `tui.altScreen.halfPageDown` | none | Scroll the transcript down half a page |
| `tui.altScreen.lineUp` | none | Scroll the transcript up one line |
| `tui.altScreen.lineDown` | none | Scroll the transcript down one line |
| `tui.altScreen.previousPrompt` | `ctrl+shift+up`, `ctrl+up` | Jump to the previous prompt |
| `tui.altScreen.nextPrompt` | `ctrl+shift+down`, `ctrl+down` | Jump to the next prompt |
| `tui.altScreen.search` | `ctrl+shift+f` | Search the transcript |
| `tui.altScreen.searchNext` | `enter`, `ctrl+g` | Select the next match while searching |
| `tui.altScreen.searchPrevious` | `shift+enter`, `ctrl+shift+g` | Select the previous match while searching |
| `tui.altScreen.searchClose` | `escape` | Close transcript search |
| `tui.altScreen.top` | `home` | Scroll to the start of the transcript |
| `tui.altScreen.bottom` | `end` | Scroll to the end of the transcript and follow new output |

## Platform differences

Some actions use a different key on each platform, because the terminal or the
operating system reserves the macOS key. WSL uses the Windows keys, except that
`ctrl+z` still suspends and `alt+z` undoes. PiG detects WSL from `WSL_DISTRO_NAME`
or `WSL_INTEROP`.

| Action | macOS | Linux | WSL | Windows |
|---|---|---|---|---|
| `app.clipboard.pasteImage` | `ctrl+v` | `ctrl+v` | `alt+v` | `alt+v` |
| `app.suspend` | `ctrl+z` | `ctrl+z` | `ctrl+z` | none |
| `app.message.dequeue` | `alt+up` | `alt+up` | `alt+q` | `alt+q` |
| `app.message.followUp` | `alt+enter` | `alt+enter` | `ctrl+q` | `ctrl+q` |
| `app.model.cycleBackward` | `shift+ctrl+p` | `shift+ctrl+p` | `alt+p` | `alt+p` |
| `app.tree.foldOrUp` | `alt+left, ctrl+left` | `ctrl+left, alt+left` | `ctrl+left, alt+left` | `ctrl+left, alt+left` |
| `app.tree.unfoldOrDown` | `alt+right, ctrl+right` | `ctrl+right, alt+right` | `ctrl+right, alt+right` | `ctrl+right, alt+right` |
| `tui.editor.undo` | `ctrl+-` | `ctrl+-` | `alt+z` | `ctrl+z` |
| `tui.altScreen.previousPrompt` | `ctrl+shift+up, ctrl+up` | `ctrl+shift+up, ctrl+up` | `ctrl+up` | `ctrl+up` |
| `tui.altScreen.nextPrompt` | `ctrl+shift+down, ctrl+down` | `ctrl+shift+down, ctrl+down` | `ctrl+down` | `ctrl+down` |
| `tui.altScreen.search` | `ctrl+shift+f` | `ctrl+shift+f` | `ctrl+f` | `ctrl+f` |

## Actions with no default key

These actions exist but have no key until you assign one. Reach them with their
slash command, or bind them in `keybindings.json`.

| Action | Slash command |
|---|---|
| `app.session.fork` | `/fork` |
| `app.session.new` | `/new` |
| `app.session.resume` | `/resume` |
| `app.session.tree` | `/tree` |

## Key names

Write a key as its name, or as modifiers joined to the name with `+`. Use
`ctrl`, `alt` and `shift` for modifiers. Examples: `ctrl+o`, `shift+ctrl+p`,
`alt+enter`, `escape`, `ctrl+backspace`.

Terminals do not all report the same keys. If a binding does not respond, the
terminal usually consumed the key before PiG saw it. See
[terminal setup](terminal-setup.md).

## Related

- [Commands](commands.md) lists the slash commands.
- [Config](config.md) lists the files PiG reads.
- [Terminal setup](terminal-setup.md) covers keys the terminal intercepts.
