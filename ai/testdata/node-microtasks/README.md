# Node microtask probes

Each probe prints the order in which a Node construct runs microtasks, `process.nextTick` callbacks and macrotasks. A ticker chain (one microtask per step) makes reaction depth visible, and `setImmediate` stands for socket data. The Go replays in `ai/node_web_stream_test.go` and `ai/node_readable_test.go` must produce the same log.

| Probe | Node source modeled | Go model |
|---|---|---|
| `webstreams.mjs` | `lib/internal/webstreams/readablestream.js`: `read()`, `values()`, `readableStreamCancel`, byte and default controllers | `ai/node_web_stream.go` |
| `readable.mjs` | `lib/internal/streams/readable.js` `createAsyncIterator`, `lib/internal/streams/end-of-stream.js`, `lib/_http_incoming.js` | `ai/node_readable.go`, `ai/node_readable_iterator.go`, `ai/continuation_ticks.go` |

The `*.golden.json` files record the oracle Node named in `oracle-node-version` (24.19.0). That file, not `.node-version`, is what the tests compare with the goldens: CI runs the security release Node 24.22.1, whose probe output equals the goldens except `values-buffered/bytes` in `webstreams.mjs` (`next3 [3]` runs before `enqueue.t1`, where 24.19.0 runs it after), so the goldens and the Go models move to a later Node together. Regenerate one after a deliberate probe change, with the oracle Node, and update `oracle-node-version` with it:

```sh
node ai/testdata/node-microtasks/webstreams.mjs > ai/testdata/node-microtasks/webstreams.golden.json
node ai/testdata/node-microtasks/readable.mjs > ai/testdata/node-microtasks/readable.golden.json
```

`go test ./ai -run 'TestNodeWebStream|TestNodeReadable'` compares the Go replay with the golden and, when `node` on `PATH` is the oracle Node, the golden with a live run. Another Node version skips the live comparison, because Node 24.22.1 and Node 26.7.0 differ for `values().next()` after a buffered byte-stream read (`values-buffered/bytes`).
