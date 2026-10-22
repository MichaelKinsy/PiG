# Using PiG

Run `pig` in the directory you want to work in to start the interactive TUI. PiG uses that directory to discover files, instructions and configuration, and to group saved sessions. PiG commands include top-level CLI verbs, slash commands inside the TUI, and keyboard shortcuts. The interactive use follows Pi 1.4.0 ([Pi's terminal usage guide](https://github.com/earendil-works/pi/blob/v1.2.0/packages/coding-agent/docs/usage.md)).

- [Command line](commands.md) lists the CLI verbs, the generic subcommands, and the `pig docs` commands for the embedded reference bundle.
- [Slash commands](commands.md) lists the commands you type in the TUI editor.
- [Keybindings](keybindings.md) describes how to change the default shortcuts.

## Keyboard shortcuts

| Key | Action |
|---|---|
| `Enter` | Submit message. |
| `Shift+Enter` / `Ctrl+J` | Newline in editor. |
| `Esc` | Cancel the active dialog, compaction, Bash command, or model turn. An extension tool that ignores its abort signal is abandoned after 3 seconds; Pi waits for it without a limit (D111). |
| `Ctrl+C` | Clear the editor; press it again within 500 ms to exit. |
| `Ctrl+D` | Exit on empty editor. |
| `Ctrl+P` / `Shift+Ctrl+P` | Cycle to next/previous scoped model (provider-qualified). |
| `Shift+Tab` | Cycle thinking level on reasoning-capable models. |
| `Tab` | Autocomplete (slash commands, paths, mentions). |
| `Ctrl+L` | Open the model selector. |
| `Ctrl+O` | Expand or collapse tool output. |
| `Ctrl+T` | Show or hide thinking blocks. |
| `Ctrl+G` | Open the current editor buffer in `externalEditor`, `$VISUAL`, `$EDITOR`, Notepad on Windows, or `nano` elsewhere. |
| `Ctrl+X` | Copy the last assistant message, as `/copy` does. |

Extensions can register more shortcuts. An extension shortcut on a key of a reserved built-in action, such as `app.interrupt` or `tui.input.submit`, is skipped with a warning. An extension shortcut on the key of another built-in action replaces that action with a warning. When two extensions register one key, the later one wins with a warning.

## Change direction while PiG works

| What you want | Action |
|---|---|
| Adjust the current task | Type a message and press `Enter` |
| Add work after the current task | Type a message and press `Alt+Enter` (`Ctrl+Q` on Windows and WSL) |
| Return queued messages to the editor | Press `Alt+Up` (`Alt+Q` on Windows and WSL) |
| Stop the current task | Press `Escape` |

A message sent with `Enter` waits until the current response and its tool calls finish, then steers the next response. A follow-up sent with `Alt+Enter` waits until PiG finishes the current task. Returning queued messages to the editor keeps queued extension custom messages queued. Pi 1.4.0 drops those custom messages (D135, [Pi #9886](https://github.com/earendil-works/pi/issues/9886)).

## Autocomplete

Use `Tab` to accept a suggestion and `Escape` to dismiss the menu. Provider arguments for `/login` show the provider name and its authentication methods. Command-argument and fuzzy-file suggestions show descriptions when the terminal is wide enough. Ordinary directory listings show entry names without an extra path description.

Type `/` to open command completion. Symbol prefixes such as `@`, `#`, and extension-provided triggers work after whitespace or CJK punctuation. PiG combines rapid symbol keystrokes into one request after 20 ms. Press `Tab` to complete a bare path immediately.

A single forced suggestion is applied without opening a menu. Multiple suggestions stay open while you refine the prefix. `Tab` accepts the highlighted item and closes the menu. `Enter` accepts a command argument without submitting the prompt. Moving the cursor refreshes an open menu, and undo restores the text and cursor from before completion.

## Prompt history

The editor keeps the most recent 100 prompts. It ignores empty entries and consecutive duplicates after trimming surrounding whitespace. Trimming follows Pi's JavaScript rules: it removes a byte-order mark (U+FEFF) at either end but preserves the next-line character (U+0085). Editor submission uses the same rule.

Use Up at the start of the editor to browse older prompts. Use Down at the end of a recalled prompt to browse newer prompts and restore the draft.

## Word navigation and large pastes

Word movement and deletion preserve Chinese, Japanese, Thai, Lao, Khmer and Burmese dictionary boundaries and ASCII punctuation stops. A large paste appears as a compact marker. Left/Right, word actions, Backspace/Delete, and mouse positioning treat a registered marker as one unit. Manually typed marker-like text remains ordinary text.

## Editor scrolling

The editor border shows centered counts for hidden lines above or below the visible input. Narrow borders truncate the indicator without discarding the whole label. Border coloring applies after the row is laid out.

## Editor text positions

The native editor counts logical cursor columns in UTF-16 code units, as Pi does. It preserves an unpaired surrogate until later input joins or edits it. Terminal output encodes unpaired surrogates as replacement characters. Left/Right and deletion still use grapheme boundaries rather than splitting a complete emoji.

## Core-vs-extension precedence

Built-in commands always win, as in Pi. When an extension registers a slash command with a built-in name, the built-in command runs, autocomplete skips the extension command, and PiG shows a warning. When two extensions register the same name, each command gets an invocation name with a numeric suffix, such as `/deploy:1` and `/deploy:2`. The same rule applies to CLI command paths: core paths are dispatched before contributed top-level or nested paths.
