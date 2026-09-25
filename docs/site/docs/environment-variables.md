# Environment variables

PiG reads these variables at startup. Each one has a setting or a default that
covers the normal case, so set a variable only when you need to change PiG from
outside its configuration: in a container, in CI, or while debugging.

Variables named `PI_*` are read for compatibility with upstream Pi. PiG reads
both spellings where both exist.

## Location

| Variable | Effect |
|---|---|
| `PIG_HOME` | Configuration root. Default `~/.pig` |
| `XDG_CONFIG_HOME` | When `PIG_HOME` is not set, the configuration root is `$XDG_CONFIG_HOME/pig` |
| `PIG_CODING_AGENT_DIR` | Agent directory alone. Default `$PIG_HOME/agent` |

`PIG_HOME` moves settings, keybindings, sessions, trust decisions and installed
packages together. Use it to run two configurations side by side.

## Network

| Variable | Effect |
|---|---|
| `PIG_OFFLINE`, `PI_OFFLINE` | Do not check for package updates. Accepts `1`, `true` or `yes` |
| `PIG_UPDATE_URL` | Release manifest used by `pig update` |
| `PIG_UPDATE_TRUST_ROOT` | Public key that signs the release manifest |
| `PI_OAUTH_CALLBACK_HOST` | Host the OAuth callback listens on |
| `PI_SHARE_GATEWAY_URL` | Artifact upload endpoint used only when you run `/share`. Default `https://pi-in-go.dev/v1/artifacts?visibility=unlisted&title=PiG+session` |
| `PI_TELEMETRY` | Override the `enableInstallTelemetry` setting: `1`, `true` or `yes` enables it, anything else disables it. Gates the install/update ping to `https://pi-in-go.dev/api/report-install?version=<version>` (sent only after a fresh install or an update with new changelog entries) and the OpenRouter/NVIDIA/Cloudflare attribution headers. `PI_OFFLINE` also stops the ping regardless of this setting. |
| `PIG_INSTALL_TELEMETRY_URL` | Overrides the install/update ping's destination URL. No upstream equivalent; tests use it to point at a local server instead of the network. |

Set `PIG_OFFLINE` in CI. Without it, every run checks for updates and fails
slowly when the network is blocked.

## Diagnostics

| Variable | Effect |
|---|---|
| `PIG_DEBUG` | Write a debug log |
| `PIG_DEBUG_KEYS` | Log every key as PiG decodes it |
| `PIG_DEBUG_TOOLS` | Print the number of tools sent with each OpenAI Chat Completions request to stderr. Needs the exact value `1` |
| `PIG_RENDER_DEBUG` | Write render diagnostics |
| `PI_TUI_DEBUG_REDRAW` | Log why the screen was fully repainted. Needs the exact value `1` |
| `PIG_STARTUP_TRACE` | Time each startup step |
| `PIG_PROFILE` | Write Go runtime profiles when PiG exits. Takes a comma-separated list of `cpu`, `heap`, `allocs`, `block`, `mutex`, `goroutine` and `trace` |
| `PIG_PROFILE_DIR` | Directory for `PIG_PROFILE` output. Default: the working directory |

Use `PIG_DEBUG_KEYS` when a keybinding does not respond: it shows whether the key
reached PiG at all. See [terminal setup](/docs/latest/terminal-setup).

Use `PI_TUI_DEBUG_REDRAW=1` when the view jumps while output arrives. A full repaint
clears the terminal's scrollback, which moves the reader, and every cause emits
the same bytes. The log names the cause, in `$PIG_HOME/agent/pig-debug.log`. Pi
reads the same variable. PiG ignores the names `PIG_DEBUG_REDRAW` and
`PI_DEBUG_REDRAW`.

`PIG_DEBUG_TOOLS` and `PI_TUI_DEBUG_REDRAW` need the exact value `1`. The other
diagnostic variables accept any non-empty value.

## Sessions

| Variable | Effect |
|---|---|
| `PIG_CODING_AGENT_SESSION_DIR` | Directory for session storage and lookup. `--session-dir` overrides it |

## Experimental features

| Variable | Effect |
|---|---|
| `PI_EXPERIMENTAL` | Turn on experimental features when set to `1`. The footer then shows an `xp` badge |
| `PIG_EXPERIMENTAL` | Same as `PI_EXPERIMENTAL`. Accepts `1`, `true` or `yes` |

## Updates and deployments

These variables describe how PiG was installed, so `pig update` can give the right
instruction. A product deployment sets them. You do not need them for a normal
install.

| Variable | Effect |
|---|---|
| `PIG_INSTALL_TIER` | Declare the installation type when PiG cannot detect it: `container`, `image`, `immutable-binary` or `piglet-binary`. Any other value is an error |
| `PIG_IMAGE_REF` | Image reference that `pig update` names for a container or image install |
| `PIG_IMAGE_PULL_CMD` | Pull command that `pig update` prints for a container or image install |
| `PIG_REDEPLOY_INSTRUCTION` | Exact redeploy instruction that `pig update` prints for a container or image install |
| `PIG_UPDATE_ALLOW_LOOPBACK_HTTP` | Allow an `http://localhost` or loopback `PIG_UPDATE_URL`. Accepts `1`, `true` or `yes`. For testing a release server |

## Display

| Variable | Effect |
|---|---|
| `PI_HARDWARE_CURSOR` | Show the terminal's own cursor |
| `PI_CLEAR_ON_SHRINK` | Repaint when content shrinks |
| `PI_HYPERLINKS` | Override OSC 8 hyperlink detection with `1`, `0`, or `auto` |
| `PI_IMAGE_PROTOCOL` | Override inline image detection with `kitty`, `iterm2`, `none`, or `auto` |
| `PI_TRUE_COLOR` | Override true-color detection with `1`, `0`, or `auto`. With true color off, themes use the 256-color palette |

Each has a setting that does the same thing. A `terminal.hyperlinks`,
`terminal.images` or `terminal.trueColor` setting wins over its variable. See [settings](/docs/latest/settings).

## Extensions and piglets

| Variable | Effect |
|---|---|
| `PIG_PIGLET_NAME` | Piglet to run |
| `PIG_PIGLET_PATH` | Directories searched for piglets |
| `PIG_SDK_GO_ROOT` | Go SDK used when building an extension |
| `PIG_SDK_PY_ROOT` | Python SDK used when building an extension |
| `PIG_SDK_RS_ROOT` | Rust SDK used when building an extension |
| `PIG_CELL_BUILD_TIMEOUT` | Time an extension build may take |
| `PIG_BUILDERS_FILE` | Container builder configuration for `pig piglet build`. Default `$PIG_HOME/state/pigletbuild/builders.json` |
| `PIG_LOGO_GLYPHFREE` | `1` renders the logo without special glyphs in `pig extension preview-login`, and `0` keeps the glyphs. Unset, PiG drops the glyphs only in Apple Terminal |

The `PIG_SDK_*` variables point a build at an SDK checkout instead of the staged
copy. Use them while developing the SDK itself. See
[extensions](/docs/latest/extensions).

## Process markers

Pig sets two markers that every child process inherits, as Pi does:

| Variable | Effect |
|---|---|
| `AI_AGENT=pi` | Generic marker that lets tooling identify the launching agent |
| `PI_CODING_AGENT=true` | Lets a child process detect that it runs inside the coding agent |

## Related

- [Providers](/docs/latest/providers) lists the credential variables for each provider.
- [Settings](/docs/latest/settings) covers the same behavior in configuration.
- [Settings](/docs/latest/settings) lists the configuration files and keys.
- [Terminal setup](/docs/latest/terminal-setup) covers display and key problems.
