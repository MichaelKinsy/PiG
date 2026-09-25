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

## Core-vs-extension precedence

Core commands always win. If an extension registers a slash command whose name collides with a built-in, the built-in handler runs and the extension command is suppressed with a diagnostic. The same rule applies to CLI command paths: core paths are dispatched before contributed top-level or nested paths.
