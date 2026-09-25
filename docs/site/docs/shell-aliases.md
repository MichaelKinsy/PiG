# Shell Aliases

PiG runs every shell command in a new, non-interactive Bash process. Non-interactive Bash does not expand aliases, and it does not read the startup files that your interactive terminal reads. An alias that works in your terminal therefore fails inside PiG until you load it.

Two settings control this. `shellPath` chooses the Bash executable. `shellCommandPrefix` runs setup before every command.

## Which shell runs a command

| Command source | Shell |
|---|---|
| The model calls the built-in `bash` tool | The Bash executable that PiG resolves |
| You type `!command` or `!!command` in the editor | The same Bash executable |
| An RPC client sends the `bash` command | The same Bash executable |
| The model calls the `powershell` tool on Windows | PowerShell |
| An extension provides or replaces a shell tool | Whatever that extension runs |

PiG starts Bash as `bash -c "<command>"`. It resolves the executable in this order:

1. `shellPath` from [settings](/docs/latest/settings), if you set it. PiG reports `Custom shell path not found` when the file does not exist.
2. On Linux and macOS: `/bin/bash`, then `bash` on your `PATH`, then `sh`.
3. On Windows: Git Bash under `Program Files`, then `bash.exe` on your `PATH`. See [Windows setup](/docs/latest/windows).

PiG does not use your `$SHELL`. The tools send Bash syntax, so a zsh or fish login shell is not a substitute.

## Choose a Bash executable

Set `shellPath` in `~/.pig/agent/settings.json` to use a specific Bash:

```json
{
  "shellPath": "~/.local/bin/bash"
}
```

PiG expands a leading `~/`. On Windows, write backslashes twice or use forward slashes:

```json
{
  "shellPath": "C:\\cygwin64\\bin\\bash.exe"
}
```

Run `/reload` after you change the setting.

## Run setup before every command

Set `shellCommandPrefix` to run shell code before each command. PiG applies it to the `bash` tool, to `!` and `!!` commands, and to RPC `bash` commands:

```json
{
  "shellCommandPrefix": "export CI=1"
}
```

PiG joins the prefix and the command with a newline. The prefix runs again for every command, so keep it fast and make sure it never waits for input.

PiG still reads `commandPrefix`, the former name of this setting.

## Enable aliases

Keep the aliases that PiG needs in a small Bash file instead of loading your whole interactive configuration. Create `~/.bash_aliases`:

```bash
alias ll='ls -la'
alias gs='git status --short'
```

Turn on alias expansion and load the file:

```json
{
  "shellCommandPrefix": "shopt -s expand_aliases\nsource ~/.bash_aliases"
}
```

Run `/reload`, then test an alias from the editor:

```text
!ll
```

The output matches `ls -la`.

Write aliases in Bash syntax. Do not load `~/.zshrc` into Bash. Zsh options, functions and plugins often fail to parse in Bash, or they behave differently there.

## Troubleshooting

### The prefix works for `!` but not for an extension's tool

`shellCommandPrefix` applies to PiG's built-in Bash execution only. An extension that replaces the `bash` tool, or that runs its own shell, sets up its own environment. Read that extension's documentation.

### `shopt: command not found`

PiG fell back to `sh`, or `shellPath` points to a shell that is not Bash. Install Bash, or set `shellPath` to a Bash executable.

### Every command hangs

A command in `shellCommandPrefix` waits for input. Remove interactive commands from the prefix.

### An alias works in the terminal but not in PiG

Check that `shellCommandPrefix` turns on `expand_aliases` before it loads the alias file. Bash reads each alias definition before it runs the next line, so the alias must be defined on a line before the command that uses it. PiG puts the prefix on its own lines before the command, which meets this rule.

## Related

- [Settings](/docs/latest/settings) lists `shellPath` and `shellCommandPrefix`.
- [Windows setup](/docs/latest/windows) covers the Windows shell defaults.
