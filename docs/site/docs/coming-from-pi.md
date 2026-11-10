# Coming from Pi

PiG is a Go implementation of [Pi](https://github.com/earendil-works/pi), the reference terminal coding agent. PiG follows Pi's observable behavior. This page is for people who already use Pi: what stays the same, what is different, and how to move over.

PiG is not an official Pi project. Michael Kinsy created PiG, which was originally developed at Hewlett Packard Enterprise. Pi's own documentation is at [pi.dev](https://pi.dev/docs/latest). PiG currently follows Pi 1.5.1.

## What stays the same

- The terminal interface, the slash commands, the keybindings and the built-in tools follow Pi. See [Slash commands](/docs/latest/slash-commands), [Keybindings](/docs/latest/keybindings) and [Using PiG](/docs/latest/usage).
- Settings, `auth.json` and session files use formats that are compatible with Pi 1.5.1. See [Configuration](/docs/latest/configuration#using-pis-directories).
- `AGENTS.md` context files, skills, prompt templates, themes and Packages work as they do in Pi. A Pi Package with a `pi` manifest in its `package.json` installs the same way.
- The modes follow Pi: interactive, print (`-p`), JSON ([JSON event stream](/docs/latest/json)) and RPC ([RPC protocol](/docs/latest/rpc)).
- The extension API and events follow Pi 1.5.1. TypeScript and JavaScript extensions run on Node with Pi's own runtime code (D73).

Every difference that a user or an extension can observe has a numbered record in the [divergence ledger](https://github.com/MichaelKinsy/PiG/blob/main/docs/parity/DIVERGENCES.md). A difference that the ledger does not name is a defect. [Report it](https://github.com/MichaelKinsy/PiG/issues).

## What is different

### Names and places

| Pi | PiG | Record |
|---|---|---|
| Command `pi` | Command `pig`. A `pig` file that you rename or copy to `pi` exits with status 2. | D2 |
| `~/.pi/agent` | `~/.pig/agent`. PiG does not read or write `~/.pi` unless you opt in. | D2 |
| `<project>/.pi` | `<project>/.pig`. PiG reads `.pig` only from the working directory. | D2 |
| `PI_CODING_AGENT_DIR` | `PIG_CODING_AGENT_DIR`. Pi's variable is read only in shared mode. | D2 |
| The startup header shows Pi's version, `v1.5.1` | The startup header and the help banner show PiG's version with the Pi release it ports, for example `v0.7.0+1.5.1`. `pig --version` prints Pi's version exactly as `pi --version` does; `pig version` prints the PiG release and the Pi release as separate fields. | D2 |
| Requests identify as `pi/<version>` | Requests identify as `pig/<version>`. PiG never identifies itself as Pi to a service. | D26, D65 |
| Hosted endpoints on pi.dev | Hosted endpoints on pi-in-go.dev, including `/share`, the model catalog, the install report and the installer. PiG reads its version check from the update manifest of the latest GitHub Release, not from pi-in-go.dev. | D39, D64 |
| `/bug` can upload a report | `/bug` writes a zip file and prints a link to a PiG issue form. | D62 |
| First-time setup in Pi's own distribution only, behind `PI_EXPERIMENTAL=1` | First-time setup, with a sprite step, on the first interactive start with a new default config directory. | D88 |

### Extensions

PiG runs a TypeScript extension in a Node subprocess with Pi's own runtime code (D73), not inside the host. Most extensions behave the same. These records describe the cases that do not:

| Difference | Record |
|---|---|
| Pi's own runtime code runs in the extension process, not in PiG's host. An object that Pi shares between its host and an extension is not shared. | D73 |
| The model-provider code that an extension calls through Pi's AI package runs in PiG's Go host, with PiG's providers. | D74 |
| Every session replacement (`/new`, `/resume`, `/fork`, `/clone`) starts the extensions in fresh processes. Module-level state resets. Pi keeps it when the working directory is the same. | D70 |
| `/reload` evaluates an edited ES module extension again. Pi keeps running the old module until it restarts. | D93 |
| Extensions in different processes share one `pi.events` bus. Timing across processes can differ from a single Node process. | D83 |
| A tool, command or event handler has no host timeout. After an abort, PiG abandons a tool that does not stop. | D56, D111 |
| Extension tool renderers and input listeners run off the UI loop. | D89, D105 |
| The Go, Rust, Python and Lisp extension SDKs have no `Codec` family, return a copy of `TuiAltScreen.children`, and serve TypeBox `Value.Check` and `Value.Errors` through the host. | D198, D207, D226 |

### Behavior

| Difference | Record |
|---|---|
| PiG ignores a setting of the wrong JSON type and prints one warning. Pi throws. | D158 |
| PiG creates session directories with mode 0700. | D132 |
| PiG stores a local Package that you type as an absolute path as you typed it. | D215 |
| PiG refuses to install an npm or Git extension root as a Package when the root holds no Package resources. | D57 |
| PiG prints no warning about its own startup writes when the config directory is read-only. | D157 |
| With `--no-stdin`, PiG skips the startup read of piped input. | D150 |

The ledger has more records than this page shows. Most cover edge cases such as terminal widths, provider wire details and exact error text.

## Install PiG next to Pi

PiG and Pi do not share files by default, so you can install PiG and keep Pi:

```bash
curl -fsSL https://pi-in-go.dev/install.sh | sh
```

You can also use `npm install -g @pi-in-go/pig`. [Quickstart](/docs/latest/quickstart) lists every route. Then run `pig`. Sign in with `/login` or set the API key variable of your provider. [Providers](/docs/latest/providers) explains the options.

## Move your Pi setup

You have two choices.

### Share Pi's directories

Run PiG against your existing Pi files:

```bash
PIG_USE_PI_DIRS=1 pig
```

PiG then uses `~/.pi/agent` (or `PI_CODING_AGENT_DIR`) and the project `.pi` directory. PiG copies nothing. Sessions, settings, credentials, Packages and extensions are the ones Pi uses. Do not edit one session from two processes at once. Only the exact value `1` turns this on. See [Using Pi's directories](/docs/latest/configuration#using-pis-directories) for the locking rules.

Use this choice to try PiG with your own sessions. Unset the variable to return to separate directories. The shared files stay where they are.

### Keep separate directories

Use this choice when you want PiG to stand alone. Copy only what you need:

| Copy from | To | Notes |
|---|---|---|
| `~/.pi/agent/settings.json` | `~/.pig/agent/settings.json` | Settings are compatible. Review `packages` and `extensions` paths that point into `~/.pi`. |
| `~/.pi/agent/auth.json` | `~/.pig/agent/auth.json` | Or sign in again with `/login`. |
| `~/.pi/agent/models.json` | `~/.pig/agent/models.json` | |
| `~/.pi/agent/keybindings.json` | `~/.pig/agent/keybindings.json` | Your key bindings. |
| `~/.pi/agent/AGENTS.md`, `SYSTEM.md`, `APPEND_SYSTEM.md` | the same names under `~/.pig/agent/` | Your instructions for every project, and your system prompt files. |
| `~/.pi/agent/extensions/`, `skills/`, `prompts/`, `themes/` | the same names under `~/.pig/agent/` | |
| `~/.pi/agent/sessions/` | `~/.pig/agent/sessions/` | Session files are compatible. Copy them when Pi is not running. |
| `<project>/.pi/` | `<project>/.pig/` | PiG looks only in `.pig`. |

Copying is your action. PiG does not copy or merge files for you (D2). Skills in `~/.agents/skills/` need no change: PiG reads them too.

A Package that hard-codes `~/.pi/agent` in its own code keeps writing there. Use shared mode for such a Package.

## Your extensions

1. Start with the extension unchanged. Load it for one run:

   ```bash
   pig -e ./my-extension.ts
   ```

   A TypeScript or JavaScript extension runs in a Node subprocess (D73). You need Node 22.13 or newer on your `PATH`. [TypeScript extension runtimes](/docs/latest/typescript-runtimes) explains the runtime choice.

2. Install a Package from npm or Git:

   ```bash
   pig install npm:<package>
   pig list
   ```

3. If the extension misbehaves, check the tables above. The records D70, D73, D74, D83 and D93 explain most differences.

4. If you want a native Go version for a single executable, follow [Port your extension](/docs/latest/port-your-extension).

## Where Pi and PiG meet

PiG tracks one pinned Pi release. The pin and the way it moves are in [the upstream pin record](https://github.com/MichaelKinsy/PiG/blob/main/docs/parity/upstream-pin.md). A change that Pi makes after the pin is not in PiG until the pin moves, unless a divergence record names it.

If you find a difference from Pi that no record names, open an issue with the Pi command and the PiG command that disagree.
