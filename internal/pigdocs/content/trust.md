# Project trust

A project can carry its own Pig configuration. That configuration can change the
model, the shell command prefix, the system prompt and the code Pig loads, so Pig
asks before it reads any of it.

Cloning a repository and starting Pig in it must not run that repository's
choices. Trust is the decision that separates the two.

## What needs trust

Pig asks when the project contains one of these inputs. Most inputs are under
`.pig/`:

| Path | What it changes |
|---|---|
| `settings.json` | any setting, including the shell prefix and the model |
| `extensions` | code Pig loads and runs |
| `skills` | instructions given to the model |
| `prompts` | instructions given to the model |
| `themes` | colors only |
| `SYSTEM.md` | the system prompt |
| `APPEND_SYSTEM.md` | text appended to the system prompt |
| `.github/copilot-instructions.md` | repository instructions that the GitHub harness source can load |

A project with none of these needs no decision, and Pig does not ask.

Until you trust a project, Pig uses your global configuration alone.

## Making the decision

Pig asks once, when a session starts in an untrusted project that has one of the
files above. Use `/trust` to record the decision for later sessions.

Decisions are stored in `~/.pig/agent/trust.json`, keyed by the project's
canonical path. A path inherits the decision of its nearest stored ancestor, so
trusting a directory trusts the repositories inside it.

## Deciding in advance

Set `defaultProjectTrust` in [settings](settings.md) to answer for every project
that has no stored decision:

| Value | Behavior |
|---|---|
| `ask` | ask each time. This is the default |
| `always` | trust every project |
| `never` | trust no project |

`always` gives every repository you open the ability to run its own extensions.
Set it only where you control what you clone.

## Related

- [Settings](settings.md) lists the keys a trusted project can set.
- [Extensions](extensions.md) covers the code a trusted project can load.
- [Commands](commands.md) lists `/trust`.
