# Slash commands

The dispatcher is exhaustive - every command here is a parity-mirrored upstream command except where noted as `[pig]`. Extensions may register additional slash commands via `register.commands[]`.

## Core

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
| `/quit` | Exit PiG. |
| `/trust` | Set the trust decision for the current project. |
| `/llama` | Manage a local llama.cpp server. |

There is no `/exit` or `/clear` command. Pi has neither, and PiG matches Pi. To
leave, use `/quit`. To clear the editor, press the `app.clear` key (`ctrl+c` by
default). `/hotkeys` lists the current bindings.

## Piglet composition

| Command | Description |
|---|---|
| `/piglet` | Inspect the Piglet active in this process. |

PiG Standard and other Piglets can add commands through selected extension
Resources. Stock PiG does not register `/runner`, `/pig-runner`, `/sprite`, or
other product commands.

## Parity harness (only when `PIG_PARITY_HARNESS=1`)

`/probe-*` family - internal scenarios used by `parity/scenarios/`. Not user-facing.
