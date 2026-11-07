# What is a Piglet

A Piglet is a small file that describes one agent. It says which model the agent prefers, which tools it can use, which extensions and skills it loads, which prompt it starts with and which secrets it needs. You write it once and PiG runs it.

A Piglet is not a fork of PiG and it is not code. It is a YAML file. PiG stays one program. The Piglet changes what that program loads and allows.

## The idea in one picture

```text
                 ┌─────────────────────────────┐
  Piglet file ──▶│ pig --piglet ./reviewer.yaml│──▶ the agent runs
  (YAML)         └─────────────────────────────┘
                              │
                              │ pig piglet build
                              ▼
                  ./pig-reviewer   one executable file
                  (the same agent, with the Piglet built in)
```

- The left path runs the agent from the file. Use it while you write and test.
- The lower path turns the same file into one executable, a Piglet Binary. Use it to hand the agent to someone who does not need to set anything up.

## What a Piglet holds

| You decide | In the file |
|---|---|
| The model | `model` |
| The tools the model may call | `tools` |
| The extensions and skills that load | `extensions`, `skills` |
| The system prompt | `systemPrompt` |
| Whether files from the user's own setup can add more | `discovery` |
| The secrets the agent needs, by name only | `secrets` |

The [Piglets](/docs/latest/piglets) page lists every field.

## A small example

This Piglet limits an agent to reading files:

```yaml
name: reader
description: "Read-only code reader"

tools: [read]

discovery:
  extensions: []
  skills: []
```

Run it:

```bash
pig --piglet ./reader.yaml
```

The agent has no `bash`, `write` or `edit` tool, and the Piglet loads no extension or skill from the user's own setup. The `tools` list is a ceiling: it removes every built-in tool outside the list and it turns none on. The active set still starts from PiG's default tools, your `defaultTools` setting or the `--tools` option. [Tool scope](/docs/latest/piglets#tool-scope) explains the rules.

## Piglet, Package, extension: how they differ

| Term | What it is |
|---|---|
| Extension | Code that adds a tool, a command or a hook to PiG. |
| Skill | Written instructions for one kind of task. |
| Package | A versioned bundle of extensions, skills, prompts and themes. It gives PiG more to use. |
| Piglet | The file that picks from those parts and sets the limits, to make one agent. |
| Piglet Binary | A Piglet built into one executable. |

A Package never turns on a Piglet. A Piglet chooses Packages.

## Why use one

- One agent per file. A team can keep a reviewer, a triage agent and a support agent as three small files in a repository.
- Fixed ceilings. The tool ceiling and the discovery rules travel with the agent. Command-line options and platform policy can narrow them. They do not widen them.
- One file to hand over. A Piglet Binary runs on another computer of the same operating system and architecture with no PiG installed. It still needs a model and credentials there. A TypeScript or JavaScript extension inside it needs Node 22.13 or newer on the `PATH`.

## Where to go next

- [Piglets](/docs/latest/piglets) has the complete field reference and the worked reviewer example.
- [Piglet Binaries](/docs/latest/piglet-binaries) explains the build, the signature checks and what runs inside the executable.
- [Port your extension](/docs/latest/port-your-extension) shows how to move a Pi extension into a Piglet.
- [Pigpen](https://github.com/MichaelKinsy/pigpen) holds Piglets and Packages you can pull and run. [Add a Piglet from Pigpen](/docs/latest/piglets#add-a-piglet-from-pigpen) shows the command.
