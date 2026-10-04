# Azure OpenAI Responses observation oracle

Pi 1.0.1 drives `packages/ai/src/api/azure-openai-responses.ts` through the OpenAI SDK 7.19.0 `AzureOpenAI` client and the shared `processResponsesStream`. Nothing here is Go output. Do not regenerate a file from PiG.

| File | Producer | Contents |
|---|---|---|
| `pi.json`, `inputs.json` | `probe.mjs` | The 108-case matrix of the parent probe (`../../probe.mjs`) reduced to this API: layers 0/1/2/runtime, text/thinking/tool, nine consumer modes. Same schema as the parent, so `TestRPC33ObservationMatrix`'s runner replays it. |
| `ticks.json` | `ticks.mjs` | Delivery-by-delivery microtask gaps and the full delivered partial for `buffered`, `pending` and `chunked` bodies, direct and through the real `ModelRuntime.streamSimple`. |
| `rpc.json` | `rpc.mjs` | The first assistant message's RPC records (`message_start`, each `message_update`, `message_end`) from the real `pi --mode rpc`, `buffered` and `pending`, eight identical runs each. |

The probe results are identical on Node 24.19.0 and 26.7.0 in every run.

`../pi-start.json` and `../server.mjs` also carry this API's start state for the shared `../check.mjs` (`node check.mjs <pig> 200 azure-openai-responses`). Go replays: `TestRPC33ObservationMatrixAzure` (`coding`), `TestRPC33AzureTickOrder` (`coding`, direct and Model Runtime), `TestAzureResponsesTickOrder` (`ai`, direct, including the start gap from the `onResponse` hook) and `TestRPCAzureOpenAIResponsesObservation` (`cmd/pig`, 200 runs of the real binary; `-rpc33-azure-full` compares every record of `rpc.json`).

## Tick measurement

A self-rescheduling `queueMicrotask` chain adds one microtask per FIFO generation and adds no await, so it never reorders Pi's reactions. It restarts at every delivery. `gap` counts the chain steps between two deliveries. The first gap starts at the synchronous `onResponse` hook (`azure-openai-responses.ts:127-128`). A gap that reaches the cap (`gapCap`, 64) means a macrotask (socket read) intervened, and the record has `io: true`.

Observed for the buffered fixture (the first delivery is `start`):

| Path | start | first block event | later block events |
|---|---:|---:|---:|
| direct provider | 4 | 17 | 6 each |
| Model Runtime | 13 | 17 | 6 each |

The pending fixture's first block event follows an I/O turn, and later events inside one chunk are 6 apart. This equals the OpenAI Responses row of the parent oracle; the Azure client adds no delivery-side tick (`json` difference against the `openai-responses` rows of `../../pi.json` is empty modulo the API name).

## Reproduce

Run from the repository root with Node 24.19.0 and the installed Pi dependency.

```bash
export PI_PACKAGE_ROOT="$(realpath extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent)"
P=coding/testdata/rpc33-observation/providers/azure-openai-responses
private=$(mktemp -d); mkdir -p "$private/home" "$private/pig" "$private/pi"
env HOME="$private/home" PIG_CODING_AGENT_DIR="$private/pig" PI_CODING_AGENT_DIR="$private/pi" node $P/probe.mjs out/pi.json out/inputs.json
env HOME="$private/home" PIG_CODING_AGENT_DIR="$private/pig" PI_CODING_AGENT_DIR="$private/pi" node $P/ticks.mjs out/ticks.json
node $P/rpc.mjs out/rpc.json 8
```
