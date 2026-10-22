# tmux Setup

PiG works inside tmux, but tmux strips modifier information from certain keys by default. Without configuration, `Shift+Enter` and `Ctrl+Enter` are usually indistinguishable from plain `Enter`. The setup is the same as for Pi 1.4.0 ([Pi's tmux guide](https://github.com/earendil-works/pi/blob/v1.2.0/packages/coding-agent/docs/tmux.md)).

## Recommended Configuration

Add to `~/.tmux.conf`:

```tmux
set -g extended-keys on
set -g extended-keys-format csi-u
```

The configuration applies to the tmux server, so start a new server. Save your work first: `tmux kill-server` ends every session of that server.

```bash
tmux kill-server
tmux
```

PiG requests extended key reporting automatically when Kitty keyboard protocol is not available. With `extended-keys-format csi-u`, tmux forwards modified keys in CSI-u format, which is the most reliable configuration. The `extended-keys-format` option requires tmux 3.5 or later.

## Verify modified keys

Start PiG inside the new tmux session and check these keys:

1. `Shift+Enter` inserts a new line in the editor.
2. `Enter` submits the prompt.
3. `Alt+Enter` queues a follow-up on macOS and Linux. Windows and WSL use `Ctrl+Q`.

If these keys still act like plain `Enter`, check that the terminal outside tmux reports modified keys. See [Terminal setup](/docs/latest/terminal-setup).

## Why `csi-u` Is Recommended

With only:

```tmux
set -g extended-keys on
```

tmux defaults to `extended-keys-format xterm`. When an application requests extended key reporting, modified keys are forwarded in xterm `modifyOtherKeys` format such as:

- `Ctrl+C` → `\x1b[27;5;99~`
- `Ctrl+D` → `\x1b[27;5;100~`
- `Ctrl+Enter` → `\x1b[27;5;13~`

With `extended-keys-format csi-u`, the same keys are forwarded as:

- `Ctrl+C` → `\x1b[99;5u`
- `Ctrl+D` → `\x1b[100;5u`
- `Ctrl+Enter` → `\x1b[13;5u`

PiG supports both formats, but `csi-u` is the recommended tmux setup.

## What This Fixes

Without tmux extended keys, modified Enter keys collapse to legacy sequences:

| Key | Without extkeys | With `csi-u` |
|-----|-----------------|--------------|
| Enter | `\r` | `\r` |
| Shift+Enter | `\r` | `\x1b[13;2u` |
| Ctrl+Enter | `\r` | `\x1b[13;5u` |
| Alt/Option+Enter | `\x1b\r` | `\x1b[13;3u` |

This affects the default keybindings (`Enter` to submit, `Shift+Enter` for newline) and any custom keybindings using modified Enter.

## Requirements

- tmux 3.5 or later for `extended-keys-format csi-u` (run `tmux -V` to check)
- A terminal emulator that supports extended keys (Ghostty, Kitty, iTerm2, WezTerm, Windows Terminal)

With tmux 3.2 through 3.4, omit `extended-keys-format csi-u`; PiG still supports tmux's default xterm `modifyOtherKeys` format.
