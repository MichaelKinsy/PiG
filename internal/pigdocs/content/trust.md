# Project trust

A project can carry its own PiG configuration. That configuration can change the
model, the shell command prefix, the system prompt and the code PiG loads, so PiG
asks before it reads any of it.

Cloning a repository and starting PiG in it must not run that repository's
choices. Trust is the decision that separates the two.

## What needs trust

PiG asks when the project contains one of these inputs. Most inputs are under
`.pig/`:

| Path | What it changes |
|---|---|
| `settings.json` | any setting, including the shell prefix and the model |
| `mcp.json` | MCP servers, including the commands of stdio servers |
| `extensions` | code PiG loads and runs |
| `skills` | instructions given to the model |
| `prompts` | instructions given to the model |
| `themes` | colors only |
| `SYSTEM.md` | the system prompt |
| `APPEND_SYSTEM.md` | text appended to the system prompt |
| `.github/copilot-instructions.md` | repository instructions that the GitHub harness source can load |

A project with none of these needs no decision, and PiG does not ask.

Until you trust a project, PiG uses your global configuration alone.

A Piglet in `.pig/piglets/` is project input too, but PiG selects a Piglet before it can ask. `--piglet <name>` uses the project's Piglet only when the project has a saved trust decision, `defaultProjectTrust` is `"always"`, or the run passes `--approve`; otherwise it uses your own Piglet of that name, and the trust prompt names the project's Piglet. A bare name is never read as a file in the working directory.

## Making the decision

PiG asks once, when a session starts in an untrusted project that has one of the
files above. Use `/trust` to record the decision for later sessions.

The selector shows the saved decision separately from the current session's trust state. The checkmark stays on the saved choice while you browse. Select **Trust parent folder** to save trust for the parent and clear the project's override. Saving does not change the current session's trust state. Restart PiG to use the new decision.

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
