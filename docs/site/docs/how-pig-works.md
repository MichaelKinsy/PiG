# How PiG works

PiG sends model requests, runs tools, assembles context and stores sessions. It follows the design of Pi 0.87.1. This page shows how those parts fit together, and it names the places where PiG's Go implementation differs from Pi.

A session is PiG's record of one conversation. It holds messages, tool calls and their results, model changes, compactions and other events. The entries form a tree. Each path from the root to an entry is a branch, and the branch that ends at the current entry is the active branch. The active branch supplies the history for the next model request.

## Agent loop

When you submit a message, PiG adds it to the active branch. PiG then builds a model request from the system prompt, the messages on the active branch, the definitions of the active tools, and the model settings. It sends the request to the selected provider.

The provider streams back an assistant message. The message can contain text, thinking and tool calls. PiG records the message, runs each tool call, and records each result. That is one turn.

PiG starts another turn when the last turn produced tool results or when a steering message is waiting. Otherwise it checks the follow-up queue. When both queues are empty, the run ends.

You can send more input while the agent runs:

| Input | When PiG delivers it |
|---|---|
| Steering message | After the current turn, before the next model request. |
| Follow-up message | After the agent has no more work, before the run ends. |

`Enter` steers and `Alt+Enter` queues a follow-up. On Windows and WSL, `Ctrl+Q` queues a follow-up. Press `Escape` to stop the run. PiG then puts the queued messages back into the editor. See [keybindings](/docs/latest/keybindings).

Before each further turn of a run, PiG checks the context size. When the context passes its limit, PiG compacts older history into a summary and then continues the run. See [compaction](/docs/latest/compaction).

## Context

The active branch supplies the conversation history. PiG turns each session entry into a model message: a user, assistant or tool-result message. Shell commands you run with `!` (but not `!!`), extension messages, branch summaries and compaction summaries become user-role text. See [message types](/docs/latest/message-types).

The system prompt has these parts:

- PiG's base instructions, or your `SYSTEM.md`;
- your `APPEND_SYSTEM.md`, when one exists;
- the content of the [context files](/docs/latest/configuration#context-files), such as `AGENTS.md`;
- the name and description of each available skill.

PiG loads the full instructions of a skill only when the model reads them. Extensions can add to the system prompt and can change the message list before each request.

Editor input passes through prompt templates and skill commands before it becomes a user message. Files you attach with `@`, images and pasted text become part of that message.

## Sessions

PiG saves each session as a JSON Lines file under `~/.pig/agent/sessions/`. Every entry has an ID and names its parent entry.

When you continue from an earlier entry, PiG starts a new branch in the same file. `/fork` and `/clone` copy history into a new session file.

PiG rebuilds the model context from the active branch. A compaction entry replaces older messages in later requests. The replaced entries stay in the file. See [sessions](/docs/latest/sessions) and [session file format](/docs/latest/session-format).

## Interfaces

Every interface uses the same agent loop and the same sessions:

| Interface | How to start it | What it does |
|---|---|---|
| Interactive | `pig` | Shows the session in a terminal UI. |
| Print | `pig -p "prompt"` | Runs the prompt and writes the final answer. |
| JSON | `pig --mode json "prompt"` | Runs the prompt and writes every event as JSON Lines. |
| RPC | `pig --mode rpc` | Reads JSON Lines commands on standard input and writes responses and events. |
| Go SDK | `github.com/MichaelKinsy/PiG/coding` | Runs sessions inside a Go program. |

Pi's SDK is a TypeScript library. PiG's SDK is a Go module. See [CLI integration](/docs/latest/cli-integration) and [Go SDK](/docs/latest/sdk).

## Tools

PiG enables four built-in tools by default: `read`, `bash`, `edit` and `write`. `grep`, `find` and `ls` are available but off by default. On Windows, PiG also offers a `powershell` tool. Use `--tools`, `--exclude-tools` or the `defaultTools` setting to change the active set.

Extensions can add tools and can replace built-in tools. PiG sends every active tool definition with each model request.

## Extensions and resources

Here PiG differs most from Pi. Pi loads TypeScript extensions into its own Node.js process. PiG does not embed a JavaScript runtime and does not load Go plugins. PiG instead starts each source extension as a separate process and talks to it over a local socket. Extensions can be written in Go, Rust, Python or Node.

PiG can run one extension per process, or pack compatible extensions of one language into a shared process. A [Piglet Binary](/docs/latest/piglet-binaries) can also compile Go extensions into the `pig` executable. Each extension registers the same way in every form. See [extensions](/docs/latest/extensions).

Because extensions run in other processes, PiG checks that each process still responds while it has work in flight. When an extension process stops responding, PiG fails that extension's pending work and keeps the others running. Pi has no equivalent check. This difference is D56.

PiG keeps one extension runner for the whole process. When you switch sessions with `/new`, `/fork` or `/resume`, the same extensions serve the next session. Pi creates a new runner for each session. This difference is D30.

Other resources work as in Pi. Skills provide instructions and supporting files. Prompt templates provide reusable message text. Themes set terminal colors. Packages distribute these resources through npm, Git or a local path. [Piglets](/docs/latest/piglets) are a PiG addition: a Piglet selects extensions, skills, tools and a model for one named agent.

## Trust and permissions

PiG asks whether you trust a project before it loads the project's settings and resources. After that decision, PiG loads the context files, which do not need trust. See [configuration](/docs/latest/configuration).

Tools run with the operating-system permissions of the `pig` process. Extension processes run as the same user. A separate process isolates crashes. It is not a security sandbox. See [security](/docs/latest/security).

## Related

- [Configuration](/docs/latest/configuration) lists the files PiG reads.
- [Concepts](/docs/latest/concepts) explains Packages, Piglets and Piglet Binaries.
- [Extensions](/docs/latest/extensions) describes the extension host.
- [Message types](/docs/latest/message-types) defines the messages in a session.
