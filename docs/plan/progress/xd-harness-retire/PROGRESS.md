# xd-harness-retire

Branch `team/smc1/xd-harness-retire`. Task: delete the code Pi 1.0.0 removed.

## Status: READY

`agent/harness/**`, `agent/search` and `internal/codingagent/tools/harness_*.go` are deleted. Their tests, the parity scripts for them and the Windows-relevant `agent/harness/env` and `pico3` tests go with them. `pico3` aliases are `internal/chord`; UUIDv7 is `ai.UUIDv7`; `combineUsage` is `internal/usagetotals`. Ledgers are updated. `go vet ./...` passes for Linux and Windows, `make lint-changed` and `make divergence-guard` are green, and `cmd/pig` passes.

## Not green, not from this change

- `durable/storage` `TestDurableStorageRuntimeBoundaries`: `d-env-tools` put `NodeExecutionEnv` in `durable/env`; upstream keeps it in `env/node`.
- `durable/harness` `TestToolProgressAndLifetime`: fails on the `d-harness-c` tip too. `TestHarnessClose`, `TestChordUsageGuide` and `TestTaskRecovery` are flaky under load.
- Node 26 rejects `--experimental-transform-types`, so the Node-oracle tests fail.
- `make generate` stops at `known-gaps` on the chord-100 and 2860 owner-approval rows. `make upstream-delta` needs `.upstream/v0.99.2`, which is absent.
- `formatversions` reports unclassified `durable/*` version fields, and `source-hygiene` flags operator paths in lane PROGRESS files.
