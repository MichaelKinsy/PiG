# Command line

This page lists the `pig` command-line verbs and subcommands, and the `pig docs` commands for the embedded reference bundle.

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

## Generic subcommands

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
| `pig extension upgrade [<name\|path>...] [--all] [--dry-run] [--summary]` | Rewrite Go extensions written for an older SDK with the SDK's tested rules: back up, show a diff, rebuild, report per extension (D109). See [Upgrade a Go extension](extensions.md#upgrade-a-go-extension-written-for-an-older-sdk). |
| `pig extension preview-login <path>` | Start exactly one extension and render the login set during `session_start`; no model session starts. |
| `pig extensions cache stats [--json]` | Inspect extension and runtime-cell cache classifications without changing use metadata. |
| `pig extensions cache prune [--retention <duration>] [--max-size <bytes>] [--failures] [--dry-run] [--json]` | Remove eligible inactive cache entries. Hard roots always remain. `--failures` also removes recorded build failures, so the next start compiles those extensions again. |
| `pig piglet prune [--keep <n>] [--max-size <size>] [--dry-run]` | Remove old built Piglet Binaries, keeping the newest `n` of each Piglet and target (default 2). Pulled installs always remain. |
| `pig reload` | Stage the embedded extension SDKs and drop extension builds an older SDK produced. |
| `pig list` | List installed Packages with Pi-compatible output. |
| `pig package list [--json]` | Inspect configured Package state without starting runtimes. |
| `pig package validate <dir> [--json]` | Validate ordinary Package source and Resource membership without installing. |
| `pig package publish [<dir>] --to npm [--yes] [--tag <dist-tag>] [--access public\|restricted] [--otp <code>]` | Optional sugar over `npm publish` for a Package: validate it, check the catalog fields and that `name@version` is free, show npm's dry run, and publish only with `--yes`. PiG never handles an npm token. |
| `pig status [--json]` | Side-effect-free Package/Resource/Piglet health and canonical path overview; invalid state exits non-zero. |
| `pig login --list [--json]` | List generic built-in and contributed authentication targets without reading credentials. |
| `pig piglet list\|show\|validate\|schema\|add\|remove\|build\|keygen\|verify\|trust` | Current Piglet YAML, registration, inspection, build, and Binary-signing surface. Owned verbs use full words. |
| `pig piglet build <name> --format script --out <path\|->` | Write an explicit source-bound entry script: a POSIX shell script, or a cmd.exe batch file on Windows (D69). Creates no PiG state or records. |
| `pig piglet build <name> --format binary --out <path> [--sign-key <private-key>]` | Build a Piglet Binary and managed v1 resolution/Binary records. The optional Ed25519 signature is checked before command dispatch. |
| `pig piglet keygen <private-key>` | Create an Ed25519 private key and `<private-key>.pub` without replacing existing files. |
| `pig piglet publish <name\|path> --to github --repo <owner/repo> --sign-key <key> [--targets os/arch,...] [--artifacts <dir>] [--tag-prefix <name>/] [--dry-run\|--yes]` | Publish signed per-target Piglet Binaries, `SHA256SUMS`, and a signed release index as one GitHub Release. Dry run until `--yes`. See [Piglet Binaries](/docs/latest/piglet-binaries#publish-to-github-releases). |
| `pig piglet publish <name\|path> --to npm [--yes] [--tag <dist-tag>] [--access public\|restricted] [--otp <code>] [--npm-name <name>] [--package-map <alias>=<ref>] [--binaries github:<owner/repo>\|--no-binaries]` | Publish Piglet source to npm so `pig piglet add npm:<name>` installs it. Local Packages become `npm:` references. Dry run until `--yes`; refuses an existing `name@version`; PiG never handles an npm token. See `pig docs show publishing`. |
| `pig piglet verify <binary>` | Verify a Piglet Binary signature offline without running it. An unsigned Binary reports unsigned and exits non-zero. |
| `pig piglet trust [list\|add\|revoke\|require]` | Manage trusted and revoked signer keys and the required-signature policy. |
| `pig piglet build <name> --format image ...` | Reserved Piglet Image shape; currently fails clearly because the Image producer is not implemented. |
| `pig config [--local]` | Open the Resource filter TUI. Press Tab to switch global and project scope. |
| `pig setup [status\|go\|container]` | Show the toolchains that extensions and Piglet builds use (default), install a verified Go toolchain under `$PIG_HOME/toolchains/go`, or show how to install a container runtime. |
| `pig verify [--json] [--checksums <file>] [--provenance] [--packages] [path...]` | Verify this binary, downloaded files, Piglet files, Packages, and extension directories by SHA-256 digest. Piglet Binary signatures are checked offline against the local trust policy. |
| `pig mcp add\|remove\|list\|login\|logout` | Configure and check MCP servers without starting a session. See [MCP servers](mcp.md#mcp-commands). |
| `pig docs [sync\|path\|list\|show <name>]` | Materialize and read the documentation bundled with Stock PiG. |

`pig install` accepts one source. Missing sources and unknown options show the ordinary single-source usage. Use `pig install --help` for the separate multi-source `--validate-only` forms (D28).

## Options

These options apply to `pig [options] [prompt]`. Run `pig --help` for the full list.

| Option | Purpose |
|---|---|
| `--provider <name>`, `--api-key <key>` | Select the provider to search for `--model` and pass its API key for this run. `--provider` requires `--model`. |
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
| `--no-mcp` | Turn off the built-in MCP support for this run: no servers connect, and there are no MCP tools or `/mcp`. It does not affect an extension that replaces the built-in MCP support. |
| `--no-skills`, `-ns` | Turn off skill discovery and loading. |
| `--prompt-template <path>` | Load a prompt template file or directory. Repeat it for more. |
| `--no-prompt-templates`, `-np` | Turn off prompt template discovery and loading. |
| `--theme <path>` | Load a theme file or directory. Repeat it for more. |
| `--use-theme <name>` | Set the initial interactive theme for this run. |
| `--no-themes` | Turn off theme discovery and loading. |
| `--no-context-files`, `-nc` | Do not load `AGENTS.md` or `CLAUDE.md` files. |
| `--export <file> [output]` | Export a session file to HTML and exit. |
| `--list-models [search]` | List available models, with optional fuzzy search. |
| `--tui-mode <mode>` | Set the TUI mode for this run: `fullscreen` (the default) or `regular`. |
| `--verbose` | Force verbose startup, overriding `quietStartup`. |
| `--approve`, `-a` | Trust project-local files for this run. |
| `--no-approve`, `-na` | Ignore project-local files for this run. |
| `--offline` | Turn off startup network operations, as `PI_OFFLINE=1` does. |
| `--piglet <name\|path>` | Run a named Piglet or the Piglet file at a path. See [Piglets](/docs/latest/piglets). |

Extensions can register their own options, such as `--plan` from a plan-mode extension.

PiG resolves local extension, Skill, prompt-template, and theme arguments relative to the launch directory. It leaves package-source and remote-URL arguments unchanged during this step.

## File arguments

Use `@path` to attach a file to the initial prompt:

```bash
pig -p @notes.txt "Summarize these notes."
```

Text files use `<file name="/absolute/path">\ncontent\n</file>\n`. PiG adds the newline before `</file>` even when the file already ends with a newline, as Pi does. It skips empty files. The first positional prompt follows the closing tag's newline without an additional separator.

## Tools

```bash
pig --tools read,grep,find,ls -p "Review the code in src/"
```

The tool options select the tools the model can call for one run. See [Settings](/docs/latest/settings#tools) to change the default selection.

| Option | Effect |
|---|---|
| `--tools`, `-t <list>` | Replace the default selection with a comma-separated allowlist. It applies to built-in, extension, and custom tools. Entries are tool names or patterns where `*` matches any characters. MCP tools are kept unless an entry starts with `mcp__` (see [MCP tools](#mcp-tools)). A list of only `+name` and `-name` entries is not an allowlist: it adds tools to or removes them from the default selection, for example `pig --tools +codemode,-write`. These entries take exact tool names, not patterns, and cannot be mixed with plain names. A tool removed with `-name` stays removed after `/reload`. |
| `--exclude-tools`, `-xt <list>` | Turn off the named tools or patterns after all other selection options, MCP tools included. It applies to built-in, extension, and custom tools. An excluded tool cannot be called. |
| `--no-builtin-tools`, `-nbt` | Turn off the default built-in tools and keep extension and custom tools. |
| `--no-tools`, `-nt` | Start with every built-in, extension, custom, and MCP tool turned off. |

Without these options, PiG enables `read`, `bash`, `edit`, and `write`, unless the `defaultTools` setting changes the set. Extension tools stay enabled. An explicit empty active tool set also stays empty in the system prompt, including after reload.

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

Two built-in extensions add tools that are off by default. `codemode` runs JavaScript that calls the other tools, and `tool_search` finds tools that are not declared to the model. The `mcp` extension turns them on when an MCP server needs them. To enable them yourself, name them in `--tools` or in the `defaultTools` setting, for example `pig --tools read,bash,edit,write,codemode`. See [Codemode and tool search](codemode.md) and [MCP servers](mcp.md).

<a id="mcp-tools"></a>

`--tools` selects the tools declared to the model. It does not remove MCP tools, whose reach is set by their [exposure](mcp.md#control-tool-exposure): `pig --tools read,codemode` keeps every MCP tool callable from codemode scripts. An MCP tool that no entry names or matches is never declared directly, whatever its exposure; only `tool_search`, if listed, can load it. Once an entry starts with `mcp__`, `--tools` filters MCP tools too, so this keeps only the tools of the `radius` server:

```sh
pig --tools read,bash,codemode,'mcp__radius__*'
```

The MCP resource tools (`list_mcp_resources`, `list_mcp_resource_templates`, `read_mcp_resource`) count as MCP tools. To remove MCP tools, use `--exclude-tools 'mcp__*'` or `--no-mcp`.

For a read-only session, allow only the tools that cannot change files:

```bash
pig --tools read,grep,find,ls
```

To keep the default set without one tool, exclude it:

```bash
pig --exclude-tools bash
```

## `pig docs`

Stock PiG materializes its reference docs into `~/.pig/docs/` so the coding
agent can read the API implemented by the running binary.

```
pig docs              # sync + summary (default)
pig docs sync         # force re-sync
pig docs path         # print docs directory
pig docs list         # list available files
pig docs show <name>  # print one doc to stdout
```

`EnsureSynced` runs at startup. A content digest marker prevents redundant
writes on warm starts and updates the materialized copy when embedded content
changes.
