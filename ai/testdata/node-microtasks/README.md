# Node microtask probes

Each probe prints the order in which a Node construct runs microtasks, `process.nextTick` callbacks and macrotasks. A ticker chain (one microtask per step) makes reaction depth visible, and `setImmediate` stands for socket data. The Go replays in `ai/node_web_stream_test.go` and `ai/node_readable_test.go` must produce the same log.

| Probe | Node source modeled | Go model |
|---|---|---|
| `webstreams.mjs` | `lib/internal/webstreams/readablestream.js`: `read()`, `values()`, `readableStreamCancel`, byte and default controllers | `ai/node_web_stream.go` |
| `readable.mjs` | `lib/internal/streams/readable.js` `createAsyncIterator`, `lib/internal/streams/end-of-stream.js`, `lib/_http_incoming.js` | `ai/node_readable.go`, `ai/node_readable_iterator.go`, `ai/continuation_ticks.go` |

The `*.golden.json` files record the pinned Node (`.node-version`). Regenerate one after a deliberate probe change:

```sh
node ai/testdata/node-microtasks/webstreams.mjs > ai/testdata/node-microtasks/webstreams.golden.json
node ai/testdata/node-microtasks/readable.mjs > ai/testdata/node-microtasks/readable.golden.json
```

`go test ./ai -run 'TestNodeWebStream|TestNodeReadable'` compares the Go replay with the golden and, when `node` on `PATH` is the pinned version, the golden with a live run. Another Node version skips the live comparison, because Node 26.7.0 differs for `values().next()` after a buffered byte-stream read (`values-buffered/bytes`).
