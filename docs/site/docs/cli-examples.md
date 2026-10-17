# Command line by example

[Command line](/docs/latest/cli) lists every verb and option. This page runs the common ones and shows what they print. Each block is a command, its output and its exit status (`exit N` after the output; 0 means success). The outputs were captured from one PiG binary under a scratch home directory, so paths read `/home/you/...` here and your own paths differ. Commands that need a network or a model say so.

Version: `0.5.0+1.2.0` in the captures below. Yours prints your own release.

## Is it installed, and what is it?

```bash
pig --version
```

```text
0.5.0+1.2.0
```

The number is `<PiG version>+<Pi version it follows>`. Pi prints only its own version, so this is a difference (D63). `pig version` is the detailed form:

```bash
pig version
```

```text
pig: 0.5.0
upstream pi: 1.2.0
go: go1.27.2
platform: linux/amd64
build: dev
```

`pig diagnose` prints where PiG keeps things. Start here when a path is not what you expected. It is not in `pig --help`. The top of its output:

```text
pig diagnostics
────────────────────────────────────────────────────────────
pig-version:       0.5.0
upstream-pin:      1.2.0
build:             dev
go runtime:        go1.27.2
platform:          linux/amd64
binary:            /usr/local/bin/pig
...
paths
────────────────────────────────────────────────────────────
config root:       /home/you/.pig
agent dir:         /home/you/.pig/agent
auth.json:         /home/you/.pig/agent/auth.json
cwd:               /home/you/project
crash log:         /home/you/.pig/agent/crashes.json
exit log:          /home/you/.pig/agent/exit.log
```

`pig setup` shows which toolchains your computer has. PiG itself needs none of them. Extensions and Piglet builds do:

```text
PiG toolchains (linux/amd64)

  go         /usr/lib/go-1.22/bin/go                      Go extension cells and native Piglet builds
  container  docker (/usr/bin/docker)                     container Piglet builds
  cargo      missing                                      Rust extension cells
  node       missing                                      Node and TypeScript extensions
  python3    /usr/bin/python3                             Python extensions
  git        /usr/bin/git                                 git: Package sources

Next steps:
  cargo      install Rust from https://rustup.rs
  node       install Node.js 22.13 or newer from https://nodejs.org

PiG runs without these tools. They are needed only for the extension languages and Piglet builds listed above.
```

`pig verify` checks the binary you are running and prints its SHA-256 digest. A development build is unsigned, and a released one shows its signature state:

```text
pig 0.5.0+1.2.0 go1.27.2 linux/amd64 , build dev

/usr/local/bin/pig (binary)
  sha256:d105f57d45d384422fbc3f7632dbf2aeb11f6e8c73dc3b65ba73cf5068e7648c
  n/a  piglet signature: unsigned (no signature: anyone who can rewrite this file can rewrite its build record)
```

## Sign in and check credentials

```bash
pig login --list
pig login --list --json
```

The first form prints a table (see [Your first ten minutes](/docs/latest/first-run#2-choose-a-model)). The second prints the same targets for a program to read: `{"targets":[{"id":"anthropic","name":"Anthropic","callbackServer":true}, ...]}`.

`pig auth check` answers whether a provider has usable credentials, with no model call. Exit 0 means ready and 1 means not ready (2 is invalid). Here no OpenAI key is set, and then one is set in the environment:

```bash
pig auth check --provider openai --json
OPENAI_API_KEY=x pig auth check --provider openai --json
```

```text
{"status":"not_ready","provider":"openai","reason":"credentials_not_configured"}
```

```text
{"status":"ready","provider":"openai","authType":"api_key"}
```

`pig auth print-api-key --provider openai` prints the key PiG would use (here `x`) for another program to use. Treat its output as a secret. `pig logout openai` removes a credential stored in `auth.json`:

```text
Logged out from openai. Credentials removed from auth.json
```

## Run one prompt without the screen

Print mode runs a prompt and exits. It needs a model. These runs use your default model. Add `--model <provider>/<model>` to pick another one. The answers below came from a scripted stand-in model, so your model's words differ.

```bash
pig -p "Summarise" @notes.txt
```

```text
The notes cover the API migration, a hire and the budget.
```

`@notes.txt` puts the file's text into the message ([Applied use cases: weekly report](/docs/latest/usecase-weekly-report)). `--continue` (`-c`) continues the previous session, so a script can ask a follow-up:

```bash
pig -c -p "And one more thing"
```

```text
The follow-up answer.
```

`--mode json` prints every event as one JSON object per line, for a program to read:

```bash
pig --mode json -p "Summarise"
```

The first lines of the output:

```text
{"type":"session","version":3,"id":"01a12bd7-6081-75aa-b191-02a07fdc9c8c","timestamp":"2026-10-11T16:41:37.665Z","cwd":"/home/you/project"}
{"type":"agent_start"}
{"type":"turn_start"}
{"type":"message_start","message":{"role":"system", ...
{"type":"message_end","message":{"role":"system", ...
{"type":"message_start","message":{"role":"user","content":[{"type":"text","text":"Summarise"}], ...
```

The events are described in [JSON mode](/docs/latest/json).

`--export` writes a saved session to an HTML page and exits, with no model and no screen:

```bash
pig --export ~/.pig/agent/sessions/--home-you-project--/2026-10-11T16-41-37-615Z_01a12bd7-604f-73fb-9fe8-0b43cc137b7c.jsonl report.html
```

```text
Exported to: report.html
```

## Look at what is installed

On a fresh install these print:

```bash
pig list
pig status
```

```text
No packages installed.
```

```text
Pig status: healthy
  paths: home=/home/you/.pig agent=/home/you/.pig/agent project=/home/you/project/.pig
  packages: 0 (user=0 project=0)
  resources: 0 (enabled=0 disabled=0)
  piglets: 0 (source=0 binary=0)
```

### Install a Package

A Package is a folder with a `package.json` and resource folders. This one carries a skill and a prompt template:

```text
mypkg/
├── package.json
├── prompts/review.md
└── skills/commit/SKILL.md
```

`prompts/review.md` and `skills/commit/SKILL.md` are short Markdown files. A front matter block names and describes each one ([Prompt templates](/docs/latest/prompt-templates), [Skills](/docs/latest/skills)):

```markdown
---
description: Review the current change
---
Review the change.
```

```markdown
---
name: commit
description: Write a commit message
---
Write a commit message.
```

`package.json`:

```json
{
  "name": "@example/team-kit",
  "version": "1.0.0",
  "keywords": ["pig-package"],
  "pi": { "skills": ["skills/*"], "prompts": ["prompts/*.md"] }
}
```

```bash
pig package validate ./mypkg
pig install ./mypkg
pig list
```

```text
Package valid: /home/you/project/mypkg
  skills: 1
  prompts: 1
```

```text
Installing ./mypkg...
Installed ./mypkg
```

```text
User packages:
  ../../project/mypkg
    /home/you/project/mypkg
```

`pig status` now reports a problem if two resources collide. Here the package's `commit` skill and a `commit` skill in `~/.pig/agent/skills` have the same name. The status turns `invalid`, names both files and exits 1 (exit 1):

```text
Pig status: invalid
  paths: home=/home/you/.pig agent=/home/you/.pig/agent project=/home/you/project/.pig
  packages: 1 (user=1 project=0)
  resources: 3 (enabled=3 disabled=0)
  piglets: 0 (source=0 binary=0)
Error: duplicate skills resource "commit" in user scope: /home/you/.pig/agent/skills/commit and /home/you/project/mypkg/skills/commit
```

Remove one of them. `pig remove ./mypkg` prints `Removing ./mypkg...` and `Removed ./mypkg`. See [Packages](/docs/latest/packages) for sources such as `npm:` and `git:`.

## MCP servers

`pig mcp` edits and checks `mcp.json` without a session. Adding a server whose program is missing is accepted, and `pig mcp list` reports the failure (exit 1):

```bash
pig mcp list
pig mcp add files -- npx -y @modelcontextprotocol/server-filesystem .
pig mcp list
pig mcp remove files
```

```text
No MCP servers configured. Add them to /home/you/.pig/agent/mcp.json or .pig/mcp.json.
```

```text
Added global MCP server "files" in /home/you/.pig/agent/mcp.json.
Check it with: pig mcp list
```

```text
files: failed (codemode, global)
  npx -y @modelcontextprotocol/server-filesystem .
  spawn npx ENOENT
```

```text
Removed global MCP server "files" from /home/you/.pig/agent/mcp.json.
```

`spawn npx ENOENT` means `npx` is not installed. See [MCP servers](/docs/latest/mcp).

## Piglets

A Piglet file defines one agent. This one is read-only and has its own instructions:

```yaml
name: reviewer
description: "Reviews a change and lists risks."

systemPrompt:
  text: "You review changes. List the risks first, then the questions."

tools: [read, grep, find, ls]
```

Check, inspect, register, list and remove it:

```bash
pig piglet validate ./agents/reviewer.yaml
pig piglet show ./agents/reviewer.yaml
pig piglet add ./agents/reviewer.yaml
pig piglet list
pig piglet remove reviewer
```

```text
OK: reviewer (./agents/reviewer.yaml): 0 extension(s), 0 skill(s)
```

```text
Piglet: reviewer
  Reviews a change and lists risks.
Source: ./agents/reviewer.yaml
Built-in tools: read, grep, find, ls

System prompt: You review changes. List the risks first, then the questions. (61 bytes)
```

```text
Added reviewer (0 extensions, 0 skills) → /home/you/.pig/piglets/reviewer.yaml
```

```text
NAME      SCOPE  SOURCE                                BINARY  IMAGE  DESCRIPTION
reviewer  user   /home/you/.pig/piglets/reviewer.yaml  -       -      Reviews a change and lists risks.
```

```text
removed source: /home/you/.pig/piglets/reviewer.yaml
```

The file format is strict: PiG rejects a field it does not know and names the line. `pig piglet schema` prints every allowed field. Two common mistakes are `system:` in place of `systemPrompt:`, and `inline:` in place of `text:`:

```text
FAIL: Piglet /home/you/project/agents/reviewer.yaml: parse piglet YAML: yaml: unmarshal errors:
  line 4: field system not found in type piglet.Piglet
FAIL: 1 validation error(s)
```

```text
FAIL: Piglet /home/you/project/agents/reviewer.yaml: parse piglet YAML: yaml: unmarshal errors:
  line 5: field inline not found in type piglet.PromptRef
FAIL: 1 validation error(s)
```

Both exit 1.

The prompt key is `text` (or `file`). Run the Piglet with `pig --piglet ./agents/reviewer.yaml` or, once registered, `pig --piglet reviewer`. Inside it, the command `/piglet` names the active Piglet:

```text
 Piglet: reviewer
 Source: /home/you/project/agents/reviewer.yaml
```

Write a script that launches it by name, for another person to run:

```bash
pig piglet build reviewer --format script --out ./run-reviewer
cat ./run-reviewer
```

```text
Piglet script written: /home/you/project/run-reviewer
```

```text
#!/bin/sh
exec pig --piglet '/home/you/.pig/piglets/reviewer.yaml' "$@"
```

`pig piglet keygen <file>` makes a signing key for Binaries, and `pig piglet trust list` shows who you trust. Without a file name, `keygen` prints `Usage: pig piglet keygen <private-key-path>` and exits 2:

```bash
pig piglet keygen ./signing.key
pig piglet trust list
```

```text
Piglet signing key ed25519:c74cf1a77b670764e896cfbf2800bb28
  private: ./signing.key (keep it secret; sign with pig piglet build <name> --format binary --sign-key ./signing.key)
  public:  ./signing.key.pub (publish it; users trust it with pig piglet trust add)
```

```text
Piglet trust store: /home/you/.pig/piglet-trust
Require signature: off (unsigned Piglet Binaries run and report unsigned)
Trusted signers:
  none
```

See [Piglets](/docs/latest/piglets) and [Piglet Binaries](/docs/latest/piglet-binaries).

## Extensions

The shortest path from nothing to a working extension is on [Your first extension](/docs/latest/first-extension).

`pig extensions cache stats` reports the build cache for extensions:

```text
Extension cache: 1 entries, 5087678 bytes
```

`pig reload` re-stages the extension SDKs that PiG embeds and drops builds that an older SDK made:

```text
pig reload: staged go (98 files) to /home/you/.pig/state/pigsdk/sdk-0cd9eb109aaf2c5f
pig reload: staged python (24 files) to /home/you/.pig/state/pigsdk/sdk-py-3400e290bc372488
pig reload: staged rust (56 files) to /home/you/.pig/state/pigsdk/sdk-rs-d045d500e4528762
pig reload: dropped 0 unreachable build(s), 0.0 MB, kept 1
```

## Update

`pig update` updates PiG itself, and it refuses when PiG cannot replace the file it is running from (a development build, or a copy placed by a package manager). It then says how (exit 1):

```text
Extensions are skipped. Run pig update --extensions to update extensions.
pig cannot self-update this installation. Executable: /usr/local/bin/pig. Reinstall it with the package manager, wrapper, or source checkout that provided it, or download a new pig binary from your provider's releases.
```

## Read the manual offline

`pig docs list` lists the embedded pages, `pig docs path` prints the folder that holds them and `pig docs show <name>` prints one page:

```bash
pig docs path
pig docs show onboarding
```

```text
/home/you/.pig/docs/bundle-7d53e1ebb619aaaf
```

The first lines of the page:

```text
# Get started with Pig

Stock Pig includes the reference documentation required to configure models,
author extensions, use Packages, and compose Piglets. ...
```

## Not shown on this page

These commands need a network, a login in a browser, a registry account or a terminal screen of their own, so the page does not print their output. Each has its own page.

| Command | Why | Where to read |
|---|---|---|
| `pig login <provider>` | Opens your provider's sign-in | [Your first ten minutes](/docs/latest/first-run) |
| `pig piglet pull`, `pig piglet update`, `pig piglet publish --to github` | Talk to GitHub Releases | [Piglet Binaries](/docs/latest/piglet-binaries) |
| `pig piglet publish --to npm`, `pig package publish` | Talk to npm, which asks for your login | [Publish a Package or Piglet](/docs/latest/publishing) |
| `pig update` to a new release | Downloads and replaces the binary | [Quickstart](/docs/latest/quickstart) |
| `pig config` | A full-screen list of resources | [Packages](/docs/latest/packages) |
| `pig extension upgrade`, `pig extension preview-login` | Rewrite or preview an extension written for another SDK | [Extensions](/docs/latest/extensions) |
| `pig piglet trust add`, `pig piglet trust require` | Change who you trust to sign Binaries | [Piglet Binaries](/docs/latest/piglet-binaries) |
