# test-faux oracle (Pi 1.1.0)

`probe.mjs` drives Pi's real pipeline for the paired fixture `test/parity/testdata/test-faux-provider.ts` (the Pi-side counterpart of PiG's `ai/test_faux.go`). `recorder.mjs` is loaded into the real `pi --mode rpc` process and registers that fixture unchanged. `pi.json` is the retained raw output (Node 24.19.0; Node 26.7.0 produced identical records). Do not replace it with Go output.

Scenarios: `text`, `tool`, `parallel` (two tool calls), `error` (an unhandled request) and `live-stream` (`TUI_LIVE_STREAM`: 24 deltas, each followed by a 40 ms timer). The plan scenarios emit every event from one `queueMicrotask(emitPlan)`; `live-stream` pushes `start` with the shared `output` object and awaits a timer between deltas.

Each result holds `rpc` (three runs of the real CLI: numbered `push` entries and `serialize` entries for the assistant records), and `direct` (`direct` and `agent` consumers behind 0-2 `lazyStream` layers with the push count at every delivery, and `ticks`, an independent microtask loop that samples the push count). The fixture's tool-call counter is module state, so every direct observation imports a fresh module instance.

```bash
export PI_PACKAGE_ROOT="$(realpath extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent)"
node coding/testdata/rpc33-observation/providers/test-faux/probe.mjs "$EVIDENCE/test-faux-pi.json" 3
```

Go replays: `TestTestFauxObservationOracle` (`ai`, includes push counts), `TestTestFauxAgentObservationOracle` (`coding`) and `TestTestFauxRPCObservation` (`cmd/pig`, the real `pig --mode rpc` binary; `PIG_TEST_FAUX_RPC_RUNS=200` repeats each scenario).
