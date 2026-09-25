# Configuration

PiG reads configuration from two places: the agent directory, which applies to every project you open, and the `.pig` directory inside a project. PiG also loads instruction files such as `AGENTS.md` from the directories around your working directory.

PiG keeps all of its state under `~/.pig`. It does not read or write Pi's `~/.pi` directory, so PiG and Pi can run side by side on one machine without sharing settings, credentials or sessions. This separation is divergence D2.

Use `/settings` in an interactive session to change common preferences. When you edit a configuration file by hand, run `/reload` so the running session picks up the change. `/reload` rereads settings, keybindings, extensions, skills, prompt templates, themes and context files.

## Configuration root

PiG chooses one configuration root at startup:

| Order | Source | Root |
|---|---|---|
| 1 | `PIG_HOME` is set and not empty | `$PIG_HOME` |
| 2 | `XDG_CONFIG_HOME` is set | `$XDG_CONFIG_HOME/pig` |
| 3 | default | `~/.pig` |

Pi reads neither `PIG_HOME` nor `XDG_CONFIG_HOME`. Its default agent directory is `~/.pi/agent`.

The root holds these directories:

| Path | Contents |
|---|---|
| `<root>/agent/` | The agent directory. See below. |
| `<root>/piglets/` | Your [Piglet](/docs/latest/piglets) source files. |
| `<root>/artifacts/piglets/` | Piglet Binaries that PiG manages. |
| `<root>/receipts/piglets/` | Piglet resolution and build records. |
| `<root>/state/<namespace>/` | State that one PiG capability owns. |
| `<root>/docs/` | The offline documentation that `pig docs` writes. |

## Agent directory

The agent directory is `<root>/agent`, which is `~/.pig/agent` by default. Set `PIG_CODING_AGENT_DIR` to move only this directory. Pi reads `PI_CODING_AGENT_DIR` for the same purpose. PiG ignores that variable.

This page writes the agent directory as `<agent-dir>`.

| Path | Purpose |
|---|---|
| `<agent-dir>/settings.json` | User [settings](/docs/latest/settings), including Package and resource declarations. |
| `<agent-dir>/keybindings.json` | Custom [keybindings](/docs/latest/keybindings). |
| `<agent-dir>/models.json` | Custom endpoints and models. See [custom providers](/docs/latest/custom-provider). |
| `<agent-dir>/auth.json` | Stored API keys and OAuth tokens. See [providers](/docs/latest/providers). |
| `<agent-dir>/trust.json` | Saved project trust decisions. See [security](/docs/latest/security). |
| `<agent-dir>/AGENTS.md` | Your instructions for every project. See [context files](#context-files). |
| `<agent-dir>/SYSTEM.md` | Replaces PiG's default system prompt. |
| `<agent-dir>/APPEND_SYSTEM.md` | Adds text to the end of the system prompt. |
| `<agent-dir>/extensions/` | User [extensions](/docs/latest/extensions). |
| `<agent-dir>/skills/` | User [skills](/docs/latest/skills). |
| `<agent-dir>/prompts/` | User [prompt templates](/docs/latest/prompt-templates). |
| `<agent-dir>/themes/` | User [themes](/docs/latest/themes). |
| `<agent-dir>/sessions/` | Saved [sessions](/docs/latest/sessions), one directory per project. |
| `<agent-dir>/bin/` | Helper binaries that PiG downloads for its tools, such as `fd` and `rg`. |
| `<agent-dir>/npm/`, `<agent-dir>/git/` | Packages installed at user scope. See [packages](/docs/latest/packages). |

PiG also finds skills in `~/.agents/skills/`. See [skills](/docs/latest/skills).

## Project directory

A project keeps its configuration in `.pig` inside the working directory. PiG reads `.pig` only from the working directory itself. It does not look for `.pig` in parent directories.

| Path | Purpose |
|---|---|
| `.pig/settings.json` | Project settings. Keys here override the same keys in `<agent-dir>/settings.json`. |
| `.pig/SYSTEM.md` | Replaces the system prompt for this project. |
| `.pig/APPEND_SYSTEM.md` | Adds project text to the end of the system prompt. |
| `.pig/extensions/` | Project extensions. |
| `.pig/skills/` | Project skills. |
| `.pig/prompts/` | Project prompt templates. |
| `.pig/themes/` | Project themes. |
| `.pig/npm/`, `.pig/git/` | Packages installed with `pig install --local`. |
| `.pig/piglets/` | Project Piglets. |

### Project trust

A project directory can contain code that runs on your machine. PiG therefore asks whether you trust a project before it reads `.pig/settings.json`, `SYSTEM.md`, `APPEND_SYSTEM.md`, extensions, skills, prompt templates or themes from it. If you do not trust the project, PiG ignores all of them.

One setting is an exception. PiG reads `sessionDir` from the project settings before it asks, because it must locate the project's sessions first.

Use `--approve` (`-a`) to trust the project for one run, or `--no-approve` (`-na`) to ignore project files for one run. Use `/trust` to save a decision. See [security](/docs/latest/security).

### System prompt files

PiG looks for `SYSTEM.md` and for `APPEND_SYSTEM.md` separately. For each name, a file in a trusted project wins over the file in the agent directory. PiG uses one file per name and does not combine the two.

The command line overrides both files. `--system-prompt` replaces the discovered `SYSTEM.md`. `--append-system-prompt` replaces the discovered `APPEND_SYSTEM.md`, and you can give it more than once. Each value can be text or a path to a file.

## Context files

Context files hold instructions for the model, such as build commands, code conventions and project rules. PiG adds their content to the system prompt.

PiG looks for context files in the agent directory, in the working directory, and in every parent directory up to the file system root. In each directory it takes the first file that exists from this list:

1. `AGENTS.override.md`
2. `AGENTS.md`
3. `AGENTS.MD`
4. `CLAUDE.md`
5. `CLAUDE.MD`

A directory contributes at most one file. `AGENTS.override.md` therefore replaces `AGENTS.md` or `CLAUDE.md` in its own directory only. It does not hide files in the agent directory or in other directories.

PiG loads the files in this order:

1. the file in the agent directory;
2. the files from the file system root down to the working directory.

The file closest to your working directory comes last, so its instructions appear after the general ones.

In a Git worktree that sits inside its main checkout, PiG skips the main checkout's context file when the worktree root has its own file with the same name. This prevents the same repository instructions from loading twice.

Context files do not need project trust. PiG loads them even when you decline to trust the project. Pi behaves the same way.

The interactive startup banner lists the loaded files on its `[Context]` line. Use `--no-context-files` (`-nc`) to load none of them for one run.

After an in-process switch to a session from another project, PiG keeps the context files of the project it started in. Pi rebuilds them for the destination project. This difference is D61.

## Related

- [Settings](/docs/latest/settings) lists every settings key.
- [Environment variables](/docs/latest/environment-variables) lists the variables PiG reads.
- [Security](/docs/latest/security) explains project trust.
- [How PiG works](/docs/latest/how-pig-works) shows where configuration enters the agent loop.
