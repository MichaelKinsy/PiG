# Extend and modify PiG

Use this page when a user wants PiG to do something it does not do. Pick the lowest rung of the ladder that solves the task. Do not change PiG source before every lower rung is ruled out.

## The ladder

<!-- This table is generated from docs/site/fragments/customize-chooser.md, which the site quickstart also shows. Edit that file and run `make generate`. -->
<!-- gen:begin customize-chooser -->
| Rung | Pick it when | Where files go | Verify | If you know Pi |
|---|---|---|---|---|
| 1. [Context files](config.md#context-files), [settings](settings.md) and [keybindings](keybindings.md) | You want standing instructions for a folder, or a built-in behavior needs a different value or key. | `AGENTS.md` in the folder or a parent, or `<agent-dir>/AGENTS.md` for every project. `~/.pig/agent/settings.json` and `~/.pig/agent/keybindings.json`. A project file `.pig/settings.json` loads only in a trusted project. | The `[Context]` line of the startup banner. `/settings` and `/hotkeys`. Ask the user to confirm: `pig diagnose` does not show trust. | `~/.pi` and `.pi/` are never read (D2). |
| 2. [Prompt templates](prompt-templates.md), [skills](skills.md) and [themes](themes.md) | The task needs a saved prompt, task instructions with supporting files, or colors, and no code. | `~/.pig/agent/prompts/`; `~/.pig/agent/skills/<name>/SKILL.md` or `~/.agents/skills/<name>/SKILL.md`; `~/.pig/agent/themes/`. | `pig status --json`, then `/reload`. | `~/.pig/skills/` is not scanned. |
| 3. [Extensions](extensions.md), [terminal UI](tui.md) and [custom providers](custom-provider.md) | The task needs a new tool, command, hook, terminal component, provider, or UI. | A project folder from `pig extension init <dir> --lang go\|python\|rust\|lisp`. Load it with `pig -e <dir>` or `pig install <dir>`. | `pig install <dir> --validate-only --json`, then `pig -e <dir>` and `/reload`. | Pi extensions are TypeScript files. Prefer Go, then Rust, then Python. |
| 4. [Packages](packages.md) | Other people or projects need the same Resources. | A folder with `package.json`; install with `pig install <source>`. | `pig list`, `pig package validate`. | A Package never activates a Piglet. |
| 5. [Piglets](piglets.md) | One named agent needs its own tools, skills, prompt, or built-in limits. | One YAML file anywhere; `pig piglet add <file>` registers it. | `pig piglet validate`, `pig piglet show`, `pig --piglet <file>`. | `tools:` is a ceiling, not a list to turn on ([strip built-ins](https://pi-in-go.dev/docs/latest/piglets#strip-built-ins)). |
| 6. [Piglet Binaries](https://pi-in-go.dev/docs/latest/piglet-binaries) | Someone must run the agent as one executable. | `pig piglet build <name> --format binary --out <path>`. | `pig piglet verify`, then run the executable. | A Binary is not always self-contained: see its prerequisites. |
| 7. PiG source | A generic host feature is missing and no lower rung can supply it. | The PiG source repository, under its own `AGENTS.md`. | The repository gates. | Do not fork PiG to register an extension. |
<!-- gen:end customize-chooser -->

## Pick a rung

1. A standing instruction, a value or a key: rung 1.
2. A procedure or a convention in words: a skill. A fixed text the user types often: a prompt template. Only colors: a theme.
3. Code that runs: an extension. A skill adds knowledge. An extension adds capability.
4. The same Resources for several people: a Package.
5. One agent with its own limits and identity: a Piglet.
6. One file to hand to others: a Piglet Binary.

## From extension to Binary

This is the linear recipe for an application built on PiG.

1. Create one focused extension per capability:

   ```bash
   pig extension init ./extensions/review --lang go
   pig install ./extensions/review --validate-only --json
   ```

2. Write the Piglet. Empty discovery lists stop user and workspace Resources from changing the application:

   ```yaml
   name: review-agent
   extensions:
     - name: review
       origins: [local:./extensions/review]
   tools: [read, grep, find]
   discovery:
     extensions: []
     skills: []
   systemPrompt:
     file: ./prompts/review.md
   ```

   `tools` is a ceiling. With this Piglet only `read` is active unless the run passes `--tools read,grep,find` ([piglet-strip](piglet-strip.md)).

3. Run it from source while you develop. Source execution gives normal validation, process isolation, and reload:

   ```bash
   pig piglet validate ./review-agent.yaml
   pig --piglet ./review-agent.yaml
   ```

4. Build a Binary only when the user needs one executable:

   ```bash
   pig piglet build ./review-agent.yaml --format binary --out ./pig-review-agent
   ```

Set `build.extensionRealization: fused` when every extension is a reviewed Go factory and the result must be one native executable. The build fails if one extension cannot fuse. Omit it to allow other languages or external services.

## Package or script before Binary

A Binary is the heaviest delivery form. Try the lighter forms first:

| The user wants | Use |
|---|---|
| The same extensions and skills on other machines | A Package: `pig install <source>`. |
| A named agent that teammates start with one command | The Piglet source. Publish it with `pig piglet publish <name> --to npm`. Recipients run `pig piglet add npm:<name>`. |
| A launcher for one Piglet | `pig piglet build <name> --format script --out <file>`. |
| One signed executable, no PiG install needed | A Piglet Binary. |

## Binary prerequisites and risks

- The native builder needs Go (`pig setup go`) and the PiG source of the same version. A release `pig` fetches its own source. A development build reports `source-unavailable`: set `PIG_SOURCE_ROOT` to a checkout.
- Without a native builder, the container builder needs Docker or Podman. It builds Linux targets only. Run `pig setup` to see what is missing.
- Build one Binary for each target. `pig piglet publish --targets os/arch,...` builds several.
- `pig piglet add <file>` copies only local Resources inside the Piglet directory. A relative origin such as `local:./x.mjs` is refused. Use a Resource directory.
- Fused extensions share the process of the application. A panic or leak in one can stop the whole application.
- A Binary can still need things it does not contain: MCP services, Node 22.13 or newer for TypeScript extensions, secrets, and credentials. Do not call it self-contained when it has such requirements.
- Strips that name a compiled-in built-in cannot be undone by an overlay: see [piglets](piglets.md#accept-overlay-piglets).

## Change PiG source

Change PiG only for generic needs: Pi parity, a stable public extension capability, Package, Piglet, or Binary infrastructure, or provider and session behavior that all compositions use. A product command, product login, or application workflow belongs in an extension and a Piglet. A source task follows the repository's own `AGENTS.md`.

## Related

- [Extensions](extensions.md), [Extension API](extension-api.md)
- [Piglets](piglets.md), [Strip built-ins and limit tools](piglet-strip.md)
- [Packages](packages.md), [Publishing](publishing.md)
