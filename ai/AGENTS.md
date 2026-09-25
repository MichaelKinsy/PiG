# AI runtime reliability

## Read first

- `../docs/project/CONTEXT.md`
- `../docs/typescript-to-go-porting.md`
- `../.upstream/current/packages/ai/src/types.ts`
- `../.upstream/current/packages/ai/src/utils/event-stream.ts`
- the matching provider under `../.upstream/current/packages/ai/src/api/`

## Contract

A Provider receives a normalized Transcript. Public Message, content, Usage, and event shapes match pinned Pi. An Event Stream preserves event order and has one terminal result. Provider setup can return an immediate error. Runtime, provider, authentication, and cancellation failures after stream creation terminate the stream.

## Failure modes

| Failure | Detection | Required result |
|---|---|---|
| Invalid JSON enters trusted provider code | Supply a non-JSON value or non-finite number | Reject it at the public boundary |
| Result depends on event consumption | Request Result without iteration after more than the former queue bound | Result completes |
| Iteration stops while waiting | Cancel the iterator context | No waiter or goroutine remains |
| Cancellation races with termination | Run concurrent Push, iteration, and Result under the race detector | One terminal result |
| A provider emits an invalid sequence | Exercise setup, start, block, delta, end, and terminal paths | Match Pi event order |
| Replay metadata crosses provider or model identity | Replay signed content through another model | Drop or transform it as Pi does |
| Consumed events remain retained | Drain a long stream | Release consumed queue entries |
| A transport consumer stalls | Stop reading subprocess frames | Apply bounded transport backpressure and cancellation |

## Evidence

Write failure cases before provider code. Use isolated tests for normalization, JSON, payload conversion, event order, and cancellation. Use a real Model Runtime path for `complete`, `stream`, and `streamSimple`. Cover every provider family. Run `go test -race ./ai`. Retain payload and event-sequence evidence. Mutation-check one event discriminator and one provider content field.
