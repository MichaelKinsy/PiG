# Using PiG

Run `pig` in the directory you want to work in to start the interactive TUI. PiG commands include top-level CLI verbs, slash commands inside the TUI, and keyboard shortcuts.

- [Command line](/docs/latest/cli) lists the CLI verbs, the generic subcommands, and the `pig docs` commands for the embedded reference bundle.
- [Slash commands](/docs/latest/slash-commands) lists the commands you type in the TUI editor.
- [Keybindings](/docs/latest/keybindings) describes how to change the default shortcuts.

## Keyboard shortcuts

| Key | Action |
|---|---|
| `Enter` | Submit message. |
| `Shift+Enter` / `Ctrl+J` | Newline in editor. |
| `Esc` | Cancel the active dialog, compaction, Bash command, or model turn. |
| `Ctrl+C` | Clear the editor; press it again within 500 ms to exit. |
| `Ctrl+D` | Exit on empty editor. |
| `Ctrl+P` / `Shift+Ctrl+P` | Cycle to next/previous scoped model (provider-qualified). |
| `Shift+Tab` | Cycle thinking level on reasoning-capable models. |
| `Tab` | Autocomplete (slash commands, paths, mentions). |
| `Ctrl+L` | Open the model selector. |
| `Ctrl+O` | Expand or collapse tool output. |
| `Ctrl+T` | Show or hide thinking blocks. |
| `Ctrl+G` | Open the current editor buffer in `externalEditor`, `$VISUAL`, `$EDITOR`, Notepad on Windows, or `nano` elsewhere. |

Extensions may install additional shortcuts via `register.shortcuts[]`. The dispatcher rejects duplicates across the host and all loaded extensions.

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

Core commands always win. If an extension registers a slash command whose name collides with a built-in, the built-in handler runs and the extension command is suppressed with a diagnostic. The same rule applies to CLI command paths: core paths are dispatched before contributed top-level or nested paths.
