# xd-coding-harness: the production coding Harness and the durable and vacation programs (Pi 1.0.0)

Branch `team/smc1/xd-coding-harness`. Base: `team/smc1/exp-durable` merged with d-harness-a, d-harness-b, d-harness-c, sg-durable-tools, d-env-tools and xd-models-iface.

## Status

READY.

## Done

- `createCodingAgentHarness` (`internal/experimental/session_worker_factory.go`) opens the pi-durable Harness over SQLite with the Model Runtime as its models, the coding registry, the Harness settings, execution environments and pi's HTTP setup. The root conversation takes the initial model only when it is created and always the agent cwd. A startup failure closes the Harness, then the environments, and reports both failures together. `openCodingHarness` and `openStandInCodingHarness` are gone; the real-worker remote-runtime cases run the production function. `experimental-remote-runtime.test.ts` is `ported` (24 of 24 cases).
- `internal/experimental/durableagent`: `harness-setup.ts` (settings, registry, environments, HTTP, initial model), `prompt.ts` (PiG identity: the preamble says pig), `sessions.ts`, `subagent.ts`, `runtime.ts`, `main.ts` arguments.
- `internal/experimental/vacation`: `vacation.ts`, `harness-setup.ts`, and the vacation profile of `runtime.ts` and `sessions.ts`.
- `internal/experimental/durable_tui.go`, `durable_tui_run.go`: `tui.ts` of both programs (they are the same file upstream). The two programs are Go main packages, `durableagent/main` and `vacation/main`; they are not in the pig binary, as the upstream sources are excluded from the published package.
- Ledgers: PORT_MAP, upstream-sync, async-contracts (`make async-contracts` is clean), unit-evidence, test-mapping, coverage, Go interface inventory.

## Decisions

- The vacation planner's `runtime.ts` is the coding agent's with another registry, no environments and another root agent; `durableagent.Profile` carries those three differences, so one implementation serves both. `tui.ts` is byte for byte the same upstream and is one Go file.
- `durable/env` is split into `durable/env` (portable declarations) and `durable/env/node` (the Node environment), as pi-durable's `./env` and `./env/node` subpaths. d-storage's import-boundary test required it.
- `coding.NewServices` gets `AgentDir: codingagent.AgentDir()` in the worker and in the programs. Its default ignores `PIG_CODING_AGENT_DIR`, which `getAgentDir` honours upstream.
- Go deliveries to a `ViewState` run after the committing call returns, where upstream's microtask runs before the awaiting caller continues. The runtime's `cycleThinking` and `setModel` therefore read the committed agent document.

## Bugs found and fixed at the source

- `durable/session`: a commit rejected with "Session is closed" could be observed before the close listeners ran, so the scheduler reported a failure it treats as expected. A rejection now waits for the listeners (`TestSessionRejectionAfterCloseBeganSeesTheCloseListeners`, red first).
- `internal/experimental/services/models_provider.go`: concurrent `Activate` calls lost catalog revisions, because the revision was read before the state change. The read is part of the change now (`TestModelsConcurrentCatalogRevisionsAreDistinct` failed on the base).
- `internal/codingagent/status_line.go`: `formatTokens` truncated where `footer.ts` rounds (`Math.round`, `toFixed`). `TestFormatTokens` has the cases.
- `durable/harness` tool progress test: the `details()` ordering case depended on a 1 ms sleep.
- Lint findings in the merged durable lanes (`appendAssign`, an unsafe pointer comparison, an unused helper).

## Evidence

Mutations are listed per file in `test/parity/unit-evidence/xd-coding-harness.json`. Every test file was run with `-race` where it spawns goroutines.

## Known limits

- `make upstream-delta` cannot run here: the v0.99.2 mirror is absent.
- `make source-hygiene` reports the private paths in the earlier lanes' PROGRESS files.
- `TestNodeVendoredTuiUpstreamTests/native-clipboard-linux` needs xcb development files.
- The Node oracle tests of `internal/experimental/services` need Node 24 (`--experimental-transform-types`; Node 26 removed the flag).
