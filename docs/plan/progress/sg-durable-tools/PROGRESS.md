# sg-durable-tools

READY. Branch `team/smc1/sg-durable-tools` = agg-100 + d-harness-a, d-harness-b, smc1/d-env-tools.

The generator had nothing to take over: d-env-tools (env/node, tools/*) and d-storage (testing/storage-benchmark) finished by hand first. gen-go-stubs for `packages/durable` (10 s) matched every upstream case title in `tools.test.ts` (29/29), `env-node.test.ts` and `env-node-spill.test.ts`, except the `%s` each-case (parameterised test) and the taskkill case (not portable; see `durable/env/shell_test.go`).

Added `TestReadLimitEndingAtTheLastLineReportsNoContinuation` (not upstream; fails under `<` to `<=` in read.go).

Green: `go test -race ./durable/env ./durable/tools`, lint, windows vet, port-map-drift, interface-go-drift, testinventorycheck.
Not this lane: release-policy approval for chord delta-tracker; `go fix` in durable/harness; chord-guide and examples tests (d-env-tools, need Harness).
