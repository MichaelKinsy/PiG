# Commands

Pig commands include top-level CLI verbs, slash commands inside the TUI, and
`pig docs` commands for the embedded reference bundle.

## CLI verbs

```
pig [options] [prompt]
```

| Verb | Purpose |
|---|---|
| `pig` | Start interactive TUI in current directory. |
| `pig --print <prompt>` | One-shot: send prompt, print response, exit. |
| `pig --model <provider/model>` | Override model for this run. |
| `pig -e <path>` (repeatable) | Load extension at path. |
| `pig --skill <path>` | Load a Skill file or directory. Repeat the option for several Skills. |
| `pig --no-extensions` | Skip all extensions; useful for isolating bugs. |
| `pig --models <patterns>` | Scope Ctrl+P model cycling to comma-separated model patterns. It is not an allow list. Without `--model`, a new session starts on the saved default model when the scope includes it, and otherwise on the first matching model. |
| `pig --session-id <id>` | Use an exact session ID; may be combined with `--no-session` for provider cache affinity without disk persistence. |
| `pig --mode rpc` | Start the JSONL RPC command loop on stdin/stdout. |
| `pig --version` | Print the composite version `<PiG version>+<pinned Pi version>`. Pi prints only its own version (D63). |
| `pig version` | Detailed pig/pi/Go/platform/build banner. |
| `pig diagnose` | Print resolved config + binary identity. `pig --help` does not list it. |

### Generic subcommands

Product distributions may contribute additional top-level or nested command paths. Core command paths always win; use `pig --help` and the product's focused docs for contributed commands.

| Subcommand | Purpose |
|---|---|
| `pig login [provider]` | Authenticate a built-in or contributed target. Built-ins use `auth.json`; contributed providers may own their credential store. |
| `pig logout [provider]` | Remove credentials through the same provider/store used by login and TUI `/logout`. |
| `pig auth check --provider <provider> [--model <model>] [--json] [--credentials] [--no-refresh]` | Print `ready`, `not_ready`, or `invalid` and exit 0, 1, or 2. `--json` writes the structured result; `--credentials` emits the resolved credential when ready. Expired OAuth credentials are refreshed unless `--no-refresh` is given, which also leaves `auth.json` and its directory untouched. |
| `pig auth print-api-key --provider <provider> [--model <model>]` | Print the resolved API key for an external client. Refuses a provider configured with OAuth. |
| `pig auth print-bearer-token --provider <provider> [--model <model>] [--min-expiry <duration>]` | Print an OAuth bearer token, refreshing it when less than `--min-expiry` (default `30m`; units `ms`, `s`, `m`, `h`) remains. Refuses a provider configured with an API key. |
| `pig install <source> [-l]` | Install a package through a core or contributed resolver. `-l` installs into the project. |
| `pig remove <source> [-l]` | Remove a package from settings. `pig uninstall` is an alias. |
| `pig update [self\|<source>] [--extensions\|--all] [--force]` | Bare `pig update` updates the pig binary, as `pi update` does. `<source>` updates one package, `--extensions` updates every package, and `--all` updates packages and then pig. |
| `pig install <path> --validate-only [--json]` | Validate without installing; emits structured diagnostics. |
| `pig install --validate-only --set "<p1>,<p2>"` | Validate a piglet-style extension set. |
| `pig extension init <path> [--name <matching-name>] [--lang go\|python\|rust] [--login] [--isolated] [--force] [--json]` | Scaffold an extension that resolves the staged SDK offline (Go default). `--login` scaffolds a Go login factory with standard PiG art. |
| `pig extension preview-login <path>` | Start exactly one extension and render the login set during `session_start`; no model session starts. |
| `pig extensions cache stats [--json]` | Inspect extension and runtime-cell cache classifications without changing use metadata. |
| `pig extensions cache prune [--retention <duration>] [--max-size <bytes>] [--dry-run] [--json]` | Remove eligible inactive cache entries. Hard roots always remain. |
| `pig reload` | Stage the embedded extension SDKs and drop extension builds an older SDK produced. |
| `pig list` | List installed Packages with Pi-compatible output. |
| `pig package list [--json]` | Inspect configured Package state without starting runtimes. |
| `pig package validate <dir> [--json]` | Validate ordinary Package source and Resource membership without installing. |
| `pig status [--json]` | Side-effect-free Package/Resource/Piglet health and canonical path overview; invalid state exits non-zero. |
| `pig login --list [--json]` | List generic built-in and contributed authentication targets without reading credentials. |
| `pig piglet list\|show\|validate\|schema\|add\|remove\|build\|keygen\|verify\|trust` | Current Piglet YAML, registration, inspection, build, and Binary-signing surface. Owned verbs use full words. |
| `pig piglet build <name> --format script --out <path\|->` | Write an explicit source-bound entry script: a POSIX shell script, or a cmd.exe batch file on Windows (D69). Creates no Pig state or records. |
| `pig piglet build <name> --format binary --out <path> [--sign-key <private-key>]` | Build a Piglet Binary and managed v1 resolution/Binary records. The optional Ed25519 signature is checked before command dispatch. |
| `pig piglet keygen <private-key>` | Create an Ed25519 private key and `<private-key>.pub` without replacing existing files. |
| `pig piglet verify <binary>` | Verify a Piglet Binary signature offline without running it. An unsigned Binary reports unsigned and exits non-zero. |
| `pig piglet trust [list\|add\|revoke\|require]` | Manage trusted and revoked signer keys and the required-signature policy. |
| `pig piglet build <name> --format image ...` | Reserved Piglet Image shape; currently fails clearly because the Image producer is not implemented. |
| `pig config [--local]` | Open the Resource filter TUI. Press Tab to switch global and project scope. |
| `pig setup [status\|go\|container]` | Show the toolchains that extensions and Piglet builds use (default), install a verified Go toolchain under `$PIG_HOME/toolchains/go`, or show how to install a container runtime. |
| `pig verify [--json] [--checksums <file>] [--provenance] [--packages] [path...]` | Verify this binary, downloaded files, Piglet files, Packages, and extension directories by SHA-256 digest. Piglet Binary signatures are checked offline against the local trust policy. |
| `pig docs [sync\|path\|list\|show <name>]` | Materialize and read the documentation bundled with Stock PiG. |

### Options

These options apply to `pig [options] [prompt]`. Run `pig --help` for the full list.

| Option | Purpose |
|---|---|
| `--provider <name>`, `--api-key <key>` | Select a provider and pass its API key for this run. |
| `--thinking <level>` | Set the thinking level: `off`, `minimal`, `low`, `medium`, `high`, `xhigh`, or `max`. |
| `--system-prompt <text>` | Replace the system prompt. |
| `--append-system-prompt <text>` | Append text or a file's contents to the system prompt. Repeat it to append more. |
| `--continue`, `-c` | Continue the previous session. |
| `--resume`, `-r` | Select a session to resume. |
| `--session <path\|id>` | Use a session file or a partial session UUID. |
| `--fork <path\|id>` | Fork a session file or partial UUID into a new session. |
| `--session-dir <dir>` | Directory for session storage and lookup. |
| `--no-session` | Do not save the session. |
| `--name`, `-n <name>` | Set the session display name. |
| `--no-skills`, `-ns` | Turn off skill discovery and loading. |
| `--prompt-template <path>` | Load a prompt template file or directory. Repeat it for more. |
| `--no-prompt-templates`, `-np` | Turn off prompt template discovery and loading. |
| `--theme <path>` | Load a theme file or directory. Repeat it for more. |
| `--use-theme <name>` | Set the initial interactive theme for this run. |
| `--no-themes` | Turn off theme discovery and loading. |
| `--no-context-files`, `-nc` | Do not load `AGENTS.md` or `CLAUDE.md` files. |
| `--export <file> [output]` | Export a session file to HTML and exit. |
| `--list-models [search]` | List available models, with optional fuzzy search. |
| `--tui-mode <mode>` | Set the TUI mode for this run: `regular` or `fullscreen`. |
| `--verbose` | Force verbose startup, overriding `quietStartup`. |
| `--approve`, `-a` | Trust project-local files for this run. |
| `--no-approve`, `-na` | Ignore project-local files for this run. |
| `--offline` | Turn off startup network operations, as `PI_OFFLINE=1` does. |
| `--piglet <name\|path>` | Run a named Piglet or the Piglet file at a path. See [Piglets](piglets.md). |

Extensions can register their own options, such as `--plan` from a plan-mode extension.

### Tools

```bash
pig --tools read,grep,find,ls -p "Review the code in src/"
```

The tool options select the tools the model can call for one run. See [Settings](settings.md#tools) to change the default selection.

| Option | Effect |
|---|---|
| `--tools`, `-t <list>` | Replace the default selection with a comma-separated allowlist. It applies to built-in, extension, and custom tools. |
| `--exclude-tools`, `-xt <list>` | Turn off the named tools after all other selection options. It applies to built-in, extension, and custom tools. An excluded tool cannot be called. |
| `--no-builtin-tools`, `-nbt` | Turn off the default built-in tools and keep extension and custom tools. |
| `--no-tools`, `-nt` | Start with every built-in, extension, and custom tool turned off. |

Without these options, PiG enables `read`, `bash`, `edit`, and `write`, unless the `defaultTools` setting changes the set. Extension tools stay enabled.

| Built-in tool | Purpose | On by default |
|---|---|---|
| `read` | Read text files and supported images | yes |
| `bash` | Run shell commands | yes |
| `powershell` | Run PowerShell commands on Windows | no |
| `edit` | Replace exact text in an existing file | yes |
| `write` | Create or overwrite a file | yes |
| `grep` | Search file contents | no |
| `find` | Find paths by glob pattern | no |
| `ls` | List directory contents | no |

For a read-only session, allow only the tools that cannot change files:

```bash
pig --tools read,grep,find,ls
```

To keep the default set without one tool, exclude it:

```bash
pig --exclude-tools bash
```

### `pig docs`

Stock PiG materializes its reference docs into `~/.pig/docs/` so the coding
agent can read the API implemented by the running binary.

```
pig docs              # sync + summary (default)
pig docs sync         # force re-sync
pig docs path         # print docs directory
pig docs list         # list available files
pig docs show <name>  # print one doc to stdout
```

`EnsureSynced` runs once at startup, so the docs appear on a fresh `~/.pig` without
user action. The content digest marker (`.pig-docs-digest`) prevents redundant
writes on warm starts.

## Slash commands (TUI)

The dispatcher is exhaustive - every command here is a parity-mirrored upstream command except where noted as `[pig]`. Extensions may register additional slash commands via `register.commands[]`.

### Core

| Command | Description |
|---|---|
| `/settings` | Open the settings menu. |
| `/model` | Open the model selector. |
| `/thinking [level]` | Set the thinking level, or open the selector without a level. |
| `/scoped-models` | Enable/disable models for Ctrl+P cycling. |
| `/login` | Configure provider authentication. |
| `/logout` | Remove stored provider authentication. |
| `/new` | Start a new session in the same cwd. |
| `/resume` | Resume a different session. |
| `/fork` | Fork from a previous user message. |
| `/clone` | Duplicate session at the current position. |
| `/tree` | Navigate session tree. |
| `/compact` | Manually compact the context. |
| `/reload` | Reload keybindings, extensions, skills, prompts, themes, and context files. |
| `/reload --explain` | Same plus a placement/cell report. [pig] |
| `/export [path]` | Export session (default HTML; specify `.jsonl`). |
| `/import <path>` | Import and resume a JSONL session. |
| `/share` | Upload an unlisted Session share that expires after 30 days. |
| `/bug [description]` | Write a bug report archive to the current directory and print a prefilled PiG issue link to attach it to. Nothing is uploaded (D62). |
| `/copy` | Copy the last agent message to clipboard. |
| `/name <text>` | Set the session display name. |
| `/session` | Show session info and stats. |
| `/changelog` | Show changelog entries. |
| `/hotkeys` | List keyboard shortcuts. |
| `/quit` | Exit pig. |
| `/trust` | Set the trust decision for the current project. |
| `/llama` | Manage a local llama.cpp server. |

There is no `/exit` or `/clear` command. Pi has neither, and Pig matches Pi. To
leave, use `/quit`. To clear the editor, press the `app.clear` key (`ctrl+c` by
default). `/hotkeys` lists the current bindings.

### Piglet composition

| Command | Description |
|---|---|
| `/piglet` | Inspect the Piglet active in this process. |

PiG Standard and other Piglets can add commands through selected extension
Resources. Stock PiG does not register `/runner`, `/pig-runner`, `/sprite`, or
other product commands.

### Parity harness (only when `PIG_PARITY_HARNESS=1`)

`/probe-*` family - internal scenarios used by `parity/scenarios/`. Not user-facing.

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
