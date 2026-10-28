# Strip built-ins and limit tools

> **Differs from Pi, or easy to get wrong**
> - Pi has no Piglets. A Piglet's `tools:` is a ceiling: it removes tools but never turns one on.
> - `pig piglet show` prints the ceiling (`Built-in tools: ...`), not the tools that are active.
> - A Piglet has no field for the default active tools. Pass `--tools`, set `defaultTools`, or build a script that passes the option.
> - Pi's `--tools read,grep,find,ls` works with a Piglet only inside the ceiling.

Use this page when a tool, command, extension, or model is missing, or when you
write a Piglet that must offer less than PiG offers by default. It is the
reference for the `tools` and `strip` fields in [Piglets](piglets.md).

## Tool scope: `tools` is a ceiling

Root `tools` limits which built-in tools can be active. It does not turn any
tool on. The active set starts from PiG's default tools, your `defaultTools`
setting, or `--tools`. The Piglet then removes every tool outside its list.

| Piglet | Tools the model receives |
|---|---|
| `tools: [read, grep]`, `grep` not in the default set | `read` only |
| `tools: [read, grep]` and `--tools read,grep` | `read`, `grep` |
| `tools: []` | no built-in tool |
| no `tools` field | PiG's normal built-in tools |

Extension tools follow `extensions[].tools`. Omit it to offer all tools of that
extension. Use `[]` to offer none. Command-line options and platform policy can
narrow these lists. They never widen a Piglet.

## Strip built-ins

`strip` removes PiG built-ins by stable ID. A run without a Piglet strip list
is unchanged.

```yaml
name: lean-reviewer
strip:
  tools: [bash]
  commands: [/share, /export]
  extensions: [mcp, codemode]
  features: [themes]
```

| List | Effect |
|---|---|
| `tools` | The tool leaves the registry, the active tools, and the system prompt. The effective built-in set is root `tools` minus `strip.tools`. |
| `commands` | The slash command leaves the registry and autocomplete. Typed text goes to the model like any unknown command. |
| `extensions` | The built-in extension (`codemode`, `llama.cpp`, `mcp`, `pig-login`, `tool-search`) does not load. |
| `apis` | The models of that API are offered nowhere. `--model` naming one fails with a strip message. |
| `features` | One feature is off, for example `themes`, `skills`, `prompt-templates`, `docs`, `self-update`. |

Run `pig piglet schema` for the complete list of IDs. An unknown ID fails when
the Piglet resolves, and the error names the ID. A Piglet cannot strip every
tool and `/quit` together.

### Keep mode

`strip.keep.<list>` names the built-ins that stay. PiG strips every other ID of
that list, including IDs that a later PiG adds. One list is in deny mode or
keep mode, never both. `keep.<list>: []` keeps nothing.

```yaml
strip:
  keep:
    tools: [read, bash, edit, write]
    extensions: []
```

### Extend and widen

Strip lists merge down the `extends` chain. A child adds IDs with its own
`strip`. To re-enable an inherited ID, name it under `extends.remove.strip` and
set `extends.allowWiden: true`. Without `allowWiden`, resolution fails.

## Inspect the active Piglet

- `/piglet` prints the active Piglet. A Piglet that strips `/piglet` has no such
  command.
- `pig piglet show <name>` lists every changed slot and the scope. It prints
  `MCP tools: hidden, because ...` when the Piglet hides MCP tools, and it names
  the reason.
- A strip row ends in `stripped(runtime)` or `stripped(binary)`. `runtime`
  means a Piglet Binary links the built-in and disables it at start. `binary`
  means the Binary does not contain it. Tools, commands, `themes`, `skills`,
  and `prompt-templates` are runtime. Every other ID is binary.
- `pig piglet schema` prints the schema and the IDs.

An overlay on a Piglet Binary can undo some strips. See
[Piglet Binary](piglets.md#piglet-binary) for what an overlay may change.

## `systemPrompt` replaces PiG's prompt

A Piglet `systemPrompt` is the prompt. It does not add to PiG's prompt. PiG
appends the working directory to it. Omit `systemPrompt` to keep PiG's prompt.
