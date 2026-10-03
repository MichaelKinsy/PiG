# Faux provider oracle (Pi 1.0.0)

`probe.mjs` drives Pi's real pipeline for `packages/ai/src/providers/faux.ts` and `extension.mjs` is loaded into the real `pi --mode rpc` process. `pi.json` is the retained raw output (Node 24.19.0; Node 26.7.0 produced identical records). Do not replace it with Go output.

Fixtures (`fixtures` in `probe.mjs`) cross two body-wait modes with the content shapes text, thinking, tool, mixed, empty text, a response factory, a rejecting factory, scripted error, an unset stop reason, and an empty response queue (`responses.mjs` builds the scripted responses for both `probe.mjs` and `extension.mjs`):

- `buffered`: no `tokensPerSecond`. Every chunk waits on `queueMicrotask` (`faux.ts:scheduleChunk`), so the producer and the consumer chain interleave in the microtask queue. This is the faux equivalent of a body that is already buffered.
- `pending`: `tokensPerSecond` is set. Every chunk waits on a timer, the faux equivalent of an unresolved body read.

`tokenSize` is 1 (four characters per chunk), so no `Math.random` draw changes the schedule.

Each result holds:

- `rpc`: three runs of the real CLI. Entries are `push` (a faux `stream.push`, numbered) and `serialize` (an RPC assistant record written by `rpc-mode.ts:356`, with the number of pushes made at that tick). Timestamps are clocks; usage depends on Pi's system prompt, which embeds the absolute path of the installed Pi package (the same probe run from a shorter install path records 438 input tokens instead of 475), so `TestFauxRPCObservation` canonicalizes it.
- `direct`: `direct` (`for await`) and `agent` (`runAgentLoop`) consumers behind 0, 1 and 2 `lazyStream` layers. Every record is a copy at the delivery tick with the number of pushes made so far. `ticks` is an independent microtask loop started in the stream-creating segment; it samples the push count after each microtask and therefore fixes the producer's own reaction schedule (start offset and chunk cadence) with no consumer.

`deferred` records the same tick measurement for a deferred submission, a pending `fetchDeferred`, the final fetch and an unknown handle.

Reproduce from the repository root:

```bash
export PI_PACKAGE_ROOT="$(realpath extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent)"
node coding/testdata/rpc33-observation/providers/faux/probe.mjs "$EVIDENCE/faux-pi.json" 3
```

Go replays:

- `TestFauxObservationOracle` and `TestFauxDeferredObservationOracle` (`ai`) replay `direct`, `ticks` and `deferred`, including the push count at every delivery. They run the consumer and the provider as turns of one executor, so the synchronous segment that creates the stream owns execution as it does in one JavaScript thread.
- `TestFauxAgentObservationOracle` (`coding`) replays `agent` (delivered Agent events; the push count is not visible from outside `ai`).
- `TestFauxRPCObservation` (`cmd/pig`) replays the `rpc` assistant records through the RPC serializer. `PIG_FAUX_RPC_RUNS=200` repeats each fixture. Usage numbers are canonicalized there because they derive from each product's system prompt.
