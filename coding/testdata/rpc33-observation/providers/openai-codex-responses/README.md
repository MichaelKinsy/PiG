# openai-codex-responses SSE oracle (D82 W5)

`probe.mjs` drives Pi 1.0.2's real Codex SSE pipeline (`packages/ai/src/api/openai-codex-responses.ts`) and writes `pi.json`. Do not replace `pi.json` with Go output.

- `inproc` runs Pi's exported `stream` against a loopback server. Each client-socket data event (`external`: headers or body bytes arriving), each `push` of the `AssistantMessageEventStream` and each consumer delivery records `segment`, `tick` and the visible partial state. An `external` entry starts a segment and restarts the microtask counter chain at tick 0; `tick` counts chain steps completed when the entry is logged. Within a segment the tick distance between two entries is exact.
- `rpc` runs the real Pi CLI in `--mode rpc` and records the first assistant `message_start` the RPC writer emits.
- Fixtures: `buffered` (headers and body in one write), `pending` (headers, then the body in one later write), `chunked` (headers, then one write per SSE record). Shapes: `tool` (the RPC33 read call), `text` (two deltas), `invalid` (malformed JSON record) and `apierror` (an `error` record). A stream that ends without a terminal record is not in the tick oracle: Node closes the body in a nextTick outside any socket data event, so no counter chain can anchor it.
- `undici.mjs` prints the fetch body latencies the Go model encodes (`undici.json`); `TestCodexUndiciLatenciesMatchNodeProbe` compares them with the model's constants.

Every value is asserted identical over `runs` executions inside the probe. Node 24.19.0 (`.node-version`) and 26.7.0 produced identical logs.

```bash
export PI_PACKAGE_ROOT="$(realpath extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent)"
node coding/testdata/rpc33-observation/providers/openai-codex-responses/probe.mjs "$EVIDENCE/pi.json" 8
```

Compare against the checked-in file after removing `node` and `runs`. Review any difference before replacing the oracle.

The Go replays are `TestCodexSSEObservationMatchesNodeOracle` (`ai`, the full tick log) and `TestRPCCodexSSEStartMatchesPi` (`cmd/pig`, the real RPC binary; `PIG_D82_ORACLE_RUNS=200` for the declared 200 runs per fixture).

Codex WebSocket mode is out of scope: it stays an external completion.
