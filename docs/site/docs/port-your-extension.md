# Port your extension

This guide moves a Pi extension to a native Go extension for PiG. Most Pi extensions do not need a port, because PiG runs them as they are. Port one when you want it in a single native executable, or when you want it to run in PiG's Go process instead of a Node subprocess.

The procedure and its tools live in [Pigpen](https://github.com/MichaelKinsy/pigpen), PiG's repository of Piglets and Packages. This page describes the procedure, which Pigpen's Skill `pigpen-pi-extension-port` carries in full, and shows it on one real extension.

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

- `pig` and `pi` executables. Use the Pi release that your `pig` follows (`pig --version` prints it after the `+`).
- Go and Node. Go builds the extension and the test harness. Node runs the original.
- A checkout of Pigpen. Run `npm ci --ignore-scripts` in it.

Run every `pig` and `pi` command of the port with a temporary `HOME`, `PIG_HOME`, `PIG_CODING_AGENT_DIR` and `PI_CODING_AGENT_DIR`, so that the port never touches your own `~/.pig`. The `pigeq env` command writes a script that sets this up.

## Start the porter

The `pig-extension-porter` Piglet selects the Skill and the equivalence tools. In a Pigpen checkout:

```bash
npm run stage
pig piglet validate dist/staged/piglets/pig-extension-porter/piglet.yaml
pig --piglet dist/staged/piglets/pig-extension-porter/piglet.yaml
```

The `pig-porter` Piglet in Pigpen extends this one and adds nothing yet. Pigpen has no published release of either Piglet. Run them from a checkout as shown. [What is a Piglet](/docs/latest/what-is-a-piglet) explains the term.

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

8. **Build the binary.** `pig piglet build ... --format binary` must succeed with the extension as a fused member. Run the golden traces against the built binary.

9. **Package and publish.** Ship the extension as a Package under `components/<name>/` with `package.json`, `LICENSE`, `CREDITS.md`, `provenance.json` and the `port/` evidence. The provenance names the upstream repository, the full commit and the license. Add the `pig-package` keyword so the Package catalog can find it. [Publish a Package or Piglet](/docs/latest/publishing) covers the rest. Pigpen's `ports/ports.json` tracks each port and its status. A port reaches `review` with its evidence. A reviewer sets `done`.

## Measure

Compare the Go extension with the original running in PiG's Node subprocess on the same scripted session. Record memory and start time on a quiet machine, with the command, the hardware and the number of runs beside each figure. Report the figures in the port's README only with that context.

## Worked example: pi-posthorse

[pi-posthorse](https://github.com/fitchmultz/pi-posthorse) is an MIT-licensed Pi extension by Mitch Fultz. It rolls a session over into a new context without writing a summary. It adds four tools (`new_context`, `get_context_remaining`, `notes` and `history`), keeps its notes under `.pi/notes`, and makes no network calls. It is a good example because it uses most of the SDK: tools, events, settings, UI cards and the session branch.

The port is in Pigpen on the branch `feat/pi-posthorse`, under `components/posthorse`.

| Step | In this port |
|---|---|
| Pin | The row in `ports/ports.json` names npm `pi-posthorse` 0.10.11 and its full Git commit. The original is vendored unchanged, with its license, in `components/posthorse/port/oracle`. |
| Gap check | `pigeq gaps --ts components/posthorse/port/oracle` |
| Contract table | `components/posthorse/port/PORT.md` |
| Original's tests | `components/posthorse/port/upstream-tests.json` lists the original's test cases. Each needs a Go twin or a named reason. |
| Scenarios and golden traces | `components/posthorse/port/scenarios` and `port/golden` |
| Implementation | `components/posthorse/extensions/posthorse` |
| Piglet | `piglets/pig-posthorse` selects the Package and builds a Binary with the extension fused. |

This section will report the parity run, the mutation run and the memory and start-time measurements once the port is merged. Until then, the branch holds the evidence that exists.

## Where to look next

- Pigpen's `components/extension-port/skills/pigpen-pi-extension-port/SKILL.md` is the complete procedure with its rules.
- `components/extension-port/skills/pigpen-pi-extension-port/references/ts-to-go-patterns.md` maps TypeScript constructs to Go: events, Promises, dialogs, child processes, error text, strings and numbers, and Windows paths.
- `components/dirty-repo-guard` is a finished port of one of Pi's own example extensions. Its `port/PORT.md` shows a complete contract table.
- [Extensions](/docs/latest/extensions) and [Build extensions](/docs/latest/first-extension) cover the Go SDK.
