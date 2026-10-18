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
| A goroutine that drives a coroutine turn takes the executor's lock only after the body switched back to it, while another turn's body or reaction runs | `TestContinuationLateDriverDoesNotContinueAnotherTurnsDrain` holds the first body between its switch and its driver, runs a second turn into a reaction that does not return, then releases the driver | The late driver continues only a drain its own body holds; no body runs beside a reaction; every turn finishes and the executor is never left draining with no loop |
| A provider emits an invalid sequence | Exercise setup, start, block, delta, end, and terminal paths | Match Pi event order |
| Replay metadata crosses provider or model identity | Replay signed content through another model | Drop or transform it as Pi does |
| Consumed events remain retained | Drain a long stream | Release consumed queue entries |
| A transport consumer stalls | Stop reading subprocess frames | Apply bounded transport backpressure and cancellation |
| A provider receives a Transcript its API rejects (a tool result without its tool call, anything between a call and its results, a duplicate call id, a role order the API refuses) | Run any test, or any binary built with `-tags requestvalidity` | `internal/requestvalidity` panics at the send seam: the wire body of each JSON API, the Converse input, and the transcript every provider and faux provider receives. `PIG_REQUEST_VALIDITY_REPORT=<file>` records violations without panicking. A test that sends an invalid request on purpose uses `invalidRequest(t)` and states why |
| A request build copies the whole transcript (cost grows with every turn) | Convert a 100-turn and an 800-turn history for every API and compare the allocations per turn | A provider reads the transcript through `TranscriptContext.borrowed` and never modifies the slice, a message or anything a message references; `Messages` stays the copying accessor for callers that keep or change what they get. `TestRequestBuildLeavesTheTranscriptUnchanged` compares the transcript before and after the request build of one catalog model of every request shape, `TestRequestBuildDoesNotCopyWhatItDoesNotSend` fails when a build copies tool-result details it never sends, and `TestRequestBuildAllocationsAreLinearInTheHistory` bounds the cost per turn |

## Evidence

Write failure cases before provider code. Use isolated tests for normalization, JSON, payload conversion, event order, and cancellation. Use a real Model Runtime path for `complete`, `stream`, and `streamSimple`. Cover every provider family. Run `go test -race ./ai`. Retain payload and event-sequence evidence. Mutation-check one event discriminator and one provider content field. `test/pipeline-diff` sends seeded random histories through Pi's convertToLlm, transformMessages and API converters and through PiG's, and compares the requests on the wire; every contract rule comes from the provider's documentation and Pi's `transform-messages.ts`, cited in `internal/requestvalidity/wire.go`.
