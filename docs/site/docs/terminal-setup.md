# Terminal setup

PiG runs in any terminal. Some features need terminal support, and PiG turns each
one on only when it detects that support. This page lists what PiG detects, and
what to change when a key or an image does not work.

## Troubleshooting

| Symptom | Where to look |
|---|---|
| `Shift+Enter` submits instead of adding a line | The section for your terminal below. Inside tmux, see [tmux](#tmux). |
| `Alt+Enter` does not queue a follow-up | [WezTerm](#wezterm), [Alacritty](#alacritty) or [Windows Terminal](#windows-terminal) |
| Scrolling in fullscreen mode is slow | [iTerm2](#iterm2) |
| Links show no hover preview in fullscreen mode | [Ghostty](#ghostty) |
| An input method window appears in the wrong place | [IME candidate window](#ime-candidate-window) |
| A key works outside PiG but not inside it | [Keys that do not respond](#keys-that-do-not-respond) |
| herdr does not show PiG's state | [herdr](#herdr) |

Use `/hotkeys` to list the keys PiG has bound. `Ctrl+J` always inserts a new line, so you can use it in any terminal where `Shift+Enter` does not arrive.

## What PiG detects

PiG detects image, true-color, and hyperlink capabilities from environment variables, except for the tmux hyperlink check below. Keyboard negotiation and automatic theme detection also use terminal queries.

| Terminal | Images | True color | Hyperlinks |
|---|---|---|---|
| Kitty | yes | yes | yes |
| Ghostty | yes | yes | yes |
| WezTerm | yes | yes | yes |
| iTerm2 | yes | yes | yes |
| Warp | yes | yes | yes |
| VS Code | no | yes | yes |
| Alacritty | no | yes | yes |
| Zed | no | yes | yes |
| Windows Terminal | no | yes | yes |
| JetBrains | no | yes | no |
| Apple Terminal | no | with `COLORTERM` | no |

Inside `tmux` or `screen`, PiG turns images off. The multiplexer
sits between PiG and the terminal, so what the outer terminal accepts is not
evidence that the pane accepts it. Hyperlinks stay off in `screen`. In `tmux`,
PiG asks tmux whether the attached client forwards hyperlinks and uses them
only when it does.

PiG reads `TERM_PROGRAM`, `TERM` and `COLORTERM` first. It also accepts
`KITTY_WINDOW_ID`, `GHOSTTY_RESOURCES_DIR`, `WEZTERM_PANE`, `ITERM_SESSION_ID`,
`WARP_SESSION_ID` and `WT_SESSION` as evidence, because some terminals set only
these. A terminal it does not recognize gets true color only with
`COLORTERM=truecolor` or `COLORTERM=24bit`, and no images or hyperlinks.

To correct detection, set `PI_HYPERLINKS`, `PI_IMAGE_PROTOCOL` or `PI_TRUE_COLOR`
(see [environment variables](/docs/latest/environment-variables)), or the matching
`terminal.*` setting (see [settings](/docs/latest/settings)). The setting wins.

## Automatic theme detection

Without an explicit theme, PiG queries the terminal's default background with OSC 11. An automatic light/dark theme pair also queries the terminal's color-scheme preference. PiG consumes replies to its background queries before editor or extension input. A late OSC 11 reply does not replace a completed fallback or settle a newer query.

## Keys that do not respond

A terminal sends a key to PiG only if it does not use the key itself. When a
binding does nothing, the terminal usually consumed the key.

Check first whether the key works outside PiG. If the terminal has a command
bound to it, unbind the command or bind the PiG action to a different key. See
[keybindings](/docs/latest/keybindings).

### tmux

`tmux` must send extended keys, or modified keys such as `shift+enter` and
`ctrl+enter` arrive as their unmodified form. PiG warns at startup when it finds
the wrong setting.

Add both lines to `~/.tmux.conf` and restart tmux:

```tmux
set -g extended-keys on
set -g extended-keys-format csi-u
```

`extended-keys-format xterm` also sends modified keys, but in a format that
carries less information. PiG warns about it and works better with `csi-u`.

PiG does not warn when it cannot query tmux, because a sandbox that blocks the
query is not evidence of a wrong setting.

### Apple Terminal

PiG enables enhanced key reporting when available. If Terminal.app still sends
plain Return for `shift+enter`, PiG asks macOS whether Shift is held and
treats the key as `shift+enter`. This works only when PiG runs on the same Mac
as Terminal.app. Over SSH, PiG cannot see the local modifier state.

### Termux

Termux reports its window size differently from a desktop terminal. PiG detects
Termux with `TERMUX_VERSION` and does not repaint the whole buffer when the
window changes, because the repaint costs more than it corrects there.

## Terminal-specific setup

PiG asks the terminal for the Kitty keyboard protocol at startup and falls back to xterm `modifyOtherKeys`. Either one lets PiG tell `Shift+Enter` and `Alt+Enter` apart from `Enter`. The entries below cover terminals that need a change to send those keys.

### Kitty

Kitty supports the Kitty keyboard protocol. It needs no setup.

### iTerm2

The regular layout needs no setup.

In fullscreen mode (the default; `--tui-mode regular` or the `tuiMode` setting selects the regular layout), PiG captures the mouse, so iTerm2 sends wheel events to PiG instead of scrolling its own history. Fast trackpad gestures can then scroll only one line at a time. To change this, open **iTerm2 > Settings > Advanced**, find **Trackpad scrolls fast?** and set it to **No**. The setting applies to all of iTerm2.

### Ghostty

If `Alt+Backspace` does not delete a word, add this line to Ghostty's configuration file:

```text
keybind = alt+backspace=text:\x1b\x7f
```

The file is `~/Library/Application Support/com.mitchellh.ghostty/config` on macOS and `~/.config/ghostty/config` on Linux.

Remove a `keybind = shift+enter=text:\n` line if another tool added it. That line makes `Shift+Enter` send a raw line feed, which is the same byte as `Ctrl+J`. It appears to work in PiG, but PiG and tmux no longer receive a real `Shift+Enter`.

In fullscreen mode, PiG captures the mouse, so Ghostty does not underline links or preview their URLs on hover. Links are still clickable. Hold the modifier that Ghostty uses to bypass mouse capture to get its own link handling.

### WezTerm

WezTerm reports `Shift+Enter` without setup. To use the Kitty keyboard protocol, set `enable_kitty_keyboard` in `~/.wezterm.lua`:

```lua
local wezterm = require 'wezterm'
local config = wezterm.config_builder()
config.enable_kitty_keyboard = true
return config
```

On macOS, WezTerm uses `Option+Enter` for its own fullscreen toggle. To send it to PiG as `Alt+Enter`, add this entry to `config.keys`:

```lua
config.keys = {
  {
    key = 'Enter',
    mods = 'ALT',
    action = wezterm.action.SendString('\x1b[13;3u'),
  },
}
```

### Alacritty

Alacritty reports `Shift+Enter`. On macOS, `Option+Enter` can arrive as a plain `Enter`. Add this binding to `~/.config/alacritty/alacritty.toml` and restart Alacritty:

```toml
[[keyboard.bindings]]
key = "Enter"
mods = "Alt"
chars = "\u001b[13;3u"
```

### VS Code

If `Shift+Enter` submits in the integrated terminal, add this entry to VS Code's `keybindings.json`:

```json
{
  "key": "shift+enter",
  "command": "workbench.action.terminal.sendSequence",
  "args": { "text": "\u001b[13;2u" },
  "when": "terminalFocus"
}
```

The file is in `~/Library/Application Support/Code/User/` on macOS, `~/.config/Code/User/` on Linux and `%APPDATA%\Code\User\` on Windows.

### Zed

Add these bindings to Zed's `keymap.json`. They send `Shift+Enter` (new line), `Ctrl+-` (undo) and `Ctrl+Alt+]` (jump backward to a character) to PiG:

```json
{
  "context": "Terminal",
  "bindings": {
    "shift-enter": ["terminal::SendText", "\u001b[13;2u"],
    "ctrl--": ["terminal::SendText", "\u001b[45;5u"],
    "ctrl-alt-]": ["terminal::SendText", "\u001b[93;7u"]
  }
}
```

### Windows Terminal

On Windows and in WSL, PiG uses Windows defaults for some keys. For example, `Ctrl+Q` queues a follow-up, because Windows Terminal uses `Alt+Enter` for fullscreen. See [keybindings](/docs/latest/keybindings).

To send `Shift+Enter`, open Windows Terminal's `settings.json` (**Settings > Open JSON file**) and add this object to the `actions` array:

```json
{
  "command": { "action": "sendInput", "input": "\u001b[13;2u" },
  "keys": "shift+enter"
}
```

Close every Windows Terminal window and start it again.

To queue follow-ups with `Alt+Enter` instead, make Windows Terminal send the key the same way with `\u001b[13;3u`, then bind `app.message.followUp` to `alt+enter` in `~/.pig/agent/keybindings.json`.

### xfce4-terminal and Terminator

These terminals cannot report modified `Enter` keys. `Shift+Enter` and `Ctrl+Enter` bindings do not work there. Use `Ctrl+J` for a new line, or use a terminal with extended key support such as Kitty, Ghostty, WezTerm, iTerm2 or Windows Terminal.

### IntelliJ IDEA

The IntelliJ terminal cannot tell `Shift+Enter` from `Enter`. Use `Ctrl+J` for a new line, or run PiG in another terminal.

### herdr

PiG reports its state to [herdr](https://herdr.dev) as its own agent, through herdr's [third-party agent path](https://herdr.dev/docs/add-herdr-support/). Pigpen's herdr extension reports with `pane report-agent` under the source `custom:pig` and the agent `pig`. It includes the session's path and id, so herdr can resume a PiG session, and it releases the pane when PiG quits. Install it from Pigpen, or use the `herdr` or `pig-with-batteries` Piglet.

herdr's built-in Pi integration (`herdr integration install pi`) is a different thing. It also runs in PiG, and herdr acknowledges every report it sends, but herdr keeps those reports only for a pane whose foreground process is named `pi`. PiG's executable is named `pig`, so herdr discards them and lists no agent.

Until Pigpen's herdr extension is published, either of these makes the Pi integration work with PiG, with the integration file unchanged:

```bash
HERDR_AGENT=pi pig
```

or run PiG under the name `pi`:

```bash
ln -s "$(command -v pig)" ~/.local/bin/pi
pi
```

`HERDR_AGENT` is herdr's documented hint for wrappers. herdr reads it from the environment of the process you launch, on Linux and macOS, so set it on the command and not inside a running session. Do not export it for the whole shell, because herdr then treats every process in the pane as `pi`.

### IME candidate window

PiG draws its own cursor. Some input methods, for example CJK input in the IntelliJ terminal or in WezTerm under WSL, then place their candidate window in the wrong position. Show the terminal's cursor so the input method can follow it:

```bash
PI_HARDWARE_CURSOR=1 pig
```

The `showHardwareCursor` setting does the same.

## Images

PiG shows images when the terminal accepts the Kitty graphics protocol or the
iTerm2 protocol. In every other terminal PiG writes the file path instead, so no
information is lost.

Images are off inside `tmux` and `screen`. To see images, run PiG outside the
multiplexer, or set `PI_IMAGE_PROTOCOL` when you know the multiplexer forwards
them.

## Capability overrides

Set `PI_HYPERLINKS`, `PI_IMAGE_PROTOCOL`, or `PI_TRUE_COLOR` to override capability detection. The matching `terminal.hyperlinks`, `terminal.images`, and `terminal.trueColor` settings take precedence over environment variables. See [settings](/docs/latest/settings).

Use `showImages` or `/settings` to hide images without changing protocol detection.

## Closing an interactive session

On Unix, `SIGTERM` and `SIGHUP` request shutdown even when `/scoped-models`, `/settings`, or another selector has focus. PiG runs extension shutdown handlers before restoring the terminal. No additional keypress is needed to dismiss a selector during shutdown.

## Display problems

| Symptom | Cause | What to do |
|---|---|---|
| PiG exits with `Rendered line N exceeds terminal width` | a component wrote a row wider than the terminal | fix the component to truncate its rows to the render width; the crash log lists every rendered row with its width |
| colors look wrong | the terminal reports no true color | set `COLORTERM=truecolor` if the terminal supports it |
| a stray `[?62;22c` appears | the terminal answered a capability query late | PiG consumes this answer; report it with the terminal name |

Set `PIG_RENDER_DEBUG` to any value to write render diagnostics. Use it when you
report a display problem.

As in Pi, a row wider than the terminal is fatal only when it reaches a differential render. PiG then writes `pi-tui-crash.log` to the agent directory (the system temp directory when there is none) with the terminal width, the offending row and its width, and every rendered row, restores the terminal, prints the error with the log path, and exits with status 1. The first render, a forced full render, and the full render after a resize emit an over-wide row unchanged.

## Program status

PiG reports its state with the [Program Status Protocol (OSC 7501)](https://www.superlogical.com/rex/docs/build/program-status), so terminals and agent dashboards can show whether it is working, waiting for you, done, or failed:

| State | When |
|---|---|
| `working` | An agent run or compaction is in progress. The message is the session name. |
| `blocked` | An extension dialog or login waits for you. The message is the dialog title. |
| `done` | A run finished. The message is the session name. |
| `error` | A run ended with an error that is not retried. The message is the first line of the error. |
| `idle` | PiG started, or you cancelled the run. |

Reports never contain prompts or model output. The `app` value is `pig`. PiG sends reports only after the terminal answers the protocol's support query; tmux and screen do not forward them. Set `PI_PROGRAM_STATUS=1` to send reports without asking, or `PI_PROGRAM_STATUS=0` to turn them off.

## Related

- [Keybindings](/docs/latest/keybindings) lists the actions and their default keys.
- [Settings](/docs/latest/settings) lists the files and settings PiG reads.
