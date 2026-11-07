# Port your extension

This guide moves a Pi extension to a native Go extension for PiG. Most Pi extensions do not need a port, because PiG runs them as they are. Port one when you want it in a single native executable, or when you want it to run in PiG's Go process instead of a Node subprocess.

The procedure and its tools live in [Pigpen](https://github.com/MichaelKinsy/pigpen), PiG's repository of Piglets and Packages. This page describes the procedure, which Pigpen's Skill `pigpen-pi-extension-port` carries as numbered steps, and shows it on one finished port.

## Step 0: decide whether to port

1. Run the extension unchanged first. It may already do what you need:

   ```bash
   pig -e ./my-extension.ts
   ```

   [Coming from Pi](/docs/latest/coming-from-pi#your-extensions) lists the cases where a TypeScript extension behaves differently in PiG.

2. Check that PiG does not already ship the function. `pig docs list` and `pig --help` show what is built in. A package whose job PiG has built in is not ported. For example, MCP is built into PiG, so `pi-mcp-adapter` is skipped.

3. Check the license. You may copy and port source only if its license allows it. Keep the copyright notice.

Port to Go, not to another language. A Go extension on PiG's public SDK can be fused into a [Piglet Binary](/docs/latest/piglet-binaries). A Node extension cannot. A Go wrapper around the unchanged TypeScript is not a port.

## What you need

- `pig` and `pi` executables. Use the Pi release that your `pig` follows. `pig --version` prints it after the `+`.
- Go and Node. Go builds the extension and the test harness. Node runs the original.
- A checkout of Pigpen. Run `npm ci --ignore-scripts` in it.
- The `pigeq` command, which runs the proof steps. Build it from the Pigpen checkout:

  ```bash
  PIG_BIN=/path/to/pig npm run build:pigeq
  export PATH="$PWD/dist/bin:$PATH"
  ```

  `npm run build:pigeq` and `npm run build:piglet` refuse a `PIG_BIN` whose major and minor version differ from the version in `scripts/pig-requirement.json` in the checkout, or whose patch version is lower. Use a `pig` with that major and minor version.

- A Git checkout of the PiG source that your `pig` was built from. The binary build in step 8 compiles the extension against it. `pigeq source` makes one:

  ```bash
  pigeq source --from <PiG git repository> --rev <full commit> --out <directory>
  ```

Run every `pig` and `pi` command of the port with a temporary `HOME`, `PIG_HOME`, `PIG_CODING_AGENT_DIR` and `PI_CODING_AGENT_DIR`, so that the port never touches your own `~/.pig`. The `pigeq env` command writes a script that sets this up. It exports `PIGEQ_PIG`, `PIGEQ_PI` and `PIG_SOURCE_ROOT`, which the later commands use:

```bash
pigeq env --root <scratch> --pig <pig> --pi <pi> --source <directory> --module <extension directory> > <scratch>/env.sh
. <scratch>/env.sh
```

## Start the porter

The `pig-extension-porter` Piglet selects the Skill and the equivalence tools. Pigpen publishes signed Binaries of it and of `pig-porter`, which extends it and adds nothing yet: [pig-extension-porter 0.1.1](https://github.com/MichaelKinsy/pigpen/releases/tag/pig-extension-porter%2Fv0.1.1) and [pig-porter 0.1.1](https://github.com/MichaelKinsy/pigpen/releases/tag/pig-porter%2Fv0.1.1). Pull one with:

```bash
pig piglet pull 'github:MichaelKinsy/pigpen/pig-extension-porter@0.1.1'
```

The command prints a `binary:` line. Run the file on that line. It is a complete PiG with the Piglet built in, so it needs no `--piglet` option. It carries the PiG version it was built with, which `--version` prints. Version 0.1.1 of the porter is PiG 0.5.3, which can be older than your `pig`. [Add a Piglet from Pigpen](/docs/latest/piglets#add-a-piglet-from-pigpen) explains the pull. [Piglet Binaries](/docs/latest/piglet-binaries#pull-a-published-binary) explains the signature checks.

The proof steps need `pigeq` and the Pigpen checkout in any case. To use the Skill text of Pigpen's main branch, which can be newer than a release, run the Piglet from the checkout:

```bash
npm run stage
pig piglet validate dist/staged/piglets/pig-extension-porter/piglet.yaml
pig --piglet dist/staged/piglets/pig-extension-porter/piglet.yaml
```

[What is a Piglet](/docs/latest/what-is-a-piglet) explains the term.

In the session, give the Skill the original and the target:

```text
/skill:pigpen-pi-extension-port port <path-to-original.ts> to components/<name>
```

The Skill guides the agent. It does not certify a port and it is not an automatic translator. You review each step.

## The steps

Each step is done when its evidence exists.

1. **Gap check.** `pigeq gaps --ts <original>` compares the Pi APIs the source uses with PiG's Go SDK. A `MISSING` row blocks a faithful port until PiG adds the API or you record an approved exclusion. A `PARTIAL` row needs a scenario that shows the difference does not matter for this extension.

2. **Map every contract.** Write one row per observable contract in `port/PORT.md`: the upstream file and symbol, the inputs, outputs and side effects, the Go SDK call and the test that proves it. Cover events, tools, commands, flags, shortcuts, UI, files, network and subprocesses.

3. **Tests first.** Write two kinds of test before the Go code:
   - Go tests that drive the real SDK through a fake PiG host. They cover what scripted sessions cannot reach, such as a session with no UI.
   - Scripted scenarios recorded from the original running under Pi. The recording is the golden trace.

   If the original has its own tests, port each case with the same inputs and expectations. `pigeq twins check` fails on any original test that has no Go twin and no named reason to skip it.

4. **Red.** Run the tests against empty stubs and see them fail. Commit the tests and stubs on their own, before the implementation.

5. **Implement in Go.** Use the public SDK only. Port behavior, not syntax: keep the original's ordering, its omitted and null values, and its unit of string length (UTF-16 code units, code points, bytes or terminal cells). Return errors instead of hiding them.

6. **Prove parity.** `pigeq run` runs the same scenarios against the original under Pi and the port under PiG. The traces hold every observable effect in order: UI requests, `exec` calls, model requests, tool results, lifecycle events and the exit status. They must be identical. A difference is a port bug, a host difference to record, or a defect fix that you keep apart from the parity claim. Do not widen the normalizer or drop an event to make a scenario pass.

7. **Mutation check.** Write a list of deliberate defects, one per contract row, and run `pigeq mutate`. Each mutant must fail a test. A mutant that survives means a missing test.

8. **Build the binary.** Run `pig piglet build <staged-piglet.yaml> --format binary --targets <os>/<arch>` with `PIG_SOURCE_ROOT` set to the PiG source checkout. From a Pigpen checkout, `npm run build:piglet -- <piglet-name>` stages the Piglet first. The build must succeed with the extension as a fused member. Run the golden traces against the built binary with `pigeq check --builtin`.

9. **Package and publish.** Ship the extension as a Package under `components/<name>/` with `package.json`, `LICENSE`, `CREDITS.md`, `provenance.json` and the `port/` evidence. The provenance names the upstream repository, the full commit and the license. Add the `pig-package` keyword so the Package catalog can find it. [Publish a Package or Piglet](/docs/latest/publishing) covers the rest. Pigpen's `ports/ports.json` tracks each port and its status. A port reaches `review` with its evidence. A reviewer sets `done`.

## Measure

Compare the Go extension with the original running in PiG's Node subprocess on the same scripted session. Record memory and start time on a quiet machine, with the command, the hardware and the number of runs beside each figure. Report the figures in the port's README only with that context.

## Worked example: rpiv-todo

[rpiv-todo](https://github.com/juicesharp/rpiv-mono/tree/61904e69e1a50e12585bdf15f0310e633a62ba36/packages/rpiv-todo) is an MIT-licensed Pi extension by juicesharp. It gives the model a `todo` tool, adds a command that lists the tasks, and draws a live overlay of tasks that survives reload and compaction. Its port is `components/todo` in [Pigpen](https://github.com/MichaelKinsy/pigpen/tree/a342843dcbb0c3b4fa3ba341fee57888b7f3c6fb/components/todo) at commit `a342843d`. The figures below come from the port's record, [`port/PORT.md`](https://github.com/MichaelKinsy/pigpen/blob/a342843dcbb0c3b4fa3ba341fee57888b7f3c6fb/components/todo/port/PORT.md). That record was written against PiG 0.4.0 and the Pi release that PiG followed then.

| Step | In this port |
|---|---|
| Pin | `CREDITS.md` and `PORT.md` name the original, `@juicesharp/rpiv-todo` 2.11.0. `provenance.json` names its repository, rpiv-mono, at commit `61904e69e1a50e12585bdf15f0310e633a62ba36`, MIT. The original is vendored unchanged, with its license, in `port/oracle`. |
| Gap check | `pigeq gaps --ts port/oracle` found three gaps. None blocks the port. All are `PARTIAL`: `tool.promptSnippet`, `tool.renderCall` and `tool.renderResult`. |
| Contract table | `PORT.md` maps ten contracts, from the `todo` tool schema to the task-list command, the overlay and the session hooks. It records one approved exclusion: locales, which the port leaves in English. |
| Original's tests | `port/upstream-tests.json` lists 231 cases. The port has 209 exact twins and 22 named skips, each with its reason. |
| Scenarios and golden traces | `port/scenarios` and `port/golden` hold 16 scenarios, with traces recorded from the original under Pi. `pigeq check` passes all 16 against the golden traces. |
| Mutations | `port/mutations.json` holds 105 mutations. All are killed. The first run left 6 alive and 5 that did not build. Each survivor got a test, and each invalid mutation was rewritten. |
| Implementation | `extensions/rpiv-todo` uses only the public SDK. |
| Binary | `piglets/pig-popular` selects the Package. The build prints "Preparing fused Go members" and finishes. The Binary recorded its own traces, and they equal the Pi goldens in every event outside the model request, where only the tool list differs. |
| Findings | `PORT.md` lists host and SDK differences found on the way, for example that PiG's RPC mode emits the rows of a widget that Pi's RPC mode drops. |

The record also lists what the port does not do. The overlay is pre-rendered rows, and key order in a tool result follows Go's sorted maps. [Read `PORT.md`](https://github.com/MichaelKinsy/pigpen/blob/a342843dcbb0c3b4fa3ba341fee57888b7f3c6fb/components/todo/port/PORT.md) to see how each decision is recorded.

## Where to look next

- Pigpen's `components/extension-port/skills/pigpen-pi-extension-port/SKILL.md` is the complete procedure with its rules.
- `components/extension-port/skills/pigpen-pi-extension-port/references/ts-to-go-patterns.md` maps TypeScript constructs to Go: events, Promises, dialogs, child processes, error text, strings and numbers, and Windows paths.
- `components/dirty-repo-guard` is a finished port of one of Pi's own example extensions. Its `port/PORT.md` shows its contract table.
- [Extensions](/docs/latest/extensions) and [Build extensions](/docs/latest/first-extension) cover the Go SDK.
