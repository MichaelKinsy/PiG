# lw-quiet equivalence: unobserved message_update (slice S27, base b02c2ceabc)

## What changed

`loopHost.consumeStream` (agent/agent.go) asks `messageUpdatesObserved` for every assistant update event (text, thinking and tool-call start/delta/end). The answer is true while anything the Agent delivers events to could see a `message_update`: the `OnEvent`, `EventCh` or `AfterEvent` option, or a live listener whose `observesMessageUpdates` predicate is nil or true (`Agent.SubscribeObservingUpdates`; `Subscribe` and `SubscribeObserving` pass nil, so they always observe). The Session's predicate (coding/session.go) is true while it has a listener, an Events consumer (`DiscardEvents` not called), an extension `message_update` handler, or a termination hold (`HoldAgentEvents`).

When the answer is false, the update is not delivered. The consumer captures the update's partial (`ai.ShallowCapture`: a struct copy of the publication, no message copy and no argument parse) and records it in the Agent's state (`reduceUnobservedUpdate`). It does not refresh the event, build the shallow message, run the Agent's listeners (`Session.handleAgentEvent`: extension dispatch, its `Yield`, `emitSynchronousSessionEvent` with its `AwaitContinuation`, `emitPublishedEvent`), or take the `emitObserved` `Yield`. When the answer is true, the event is refreshed at the same tick (`ObservePublishedEvents` + `RefreshEvent` replace `ObserveEvents`, which refreshed inside the iterator before handing the event over, with nothing awaited in between) and delivered exactly as before.

## Why nothing observable changes

| Skipped step | What it did when nothing observes `message_update` | Proof |
|---|---|---|
| `RefreshEvent`, `shallowAssistantMessage` | Pure reads of the producer cell plus allocations; their result went only to the event. | `TestShallowCaptureBuildsTheCopyOfItsTick` (ai): a capture built after any number of later publications equals `ShallowCopy()` and `ShallowCopy().Observe()` taken at the capture's tick, and its retained nested objects read as the tick's copy does. |
| `Agent.reduceEventState` | Set the streaming message. | `reduceUnobservedUpdate` records the capture. `StreamingMessage` builds the same message once, on its first read. `TestStreamingMessageAfterAnUnobservedUpdate` (agent): after the provider ran ahead, `StreamingMessage` equals the message the update would have stored. The abort path (Go caller cancellation) builds the message from the same capture. |
| Agent listeners → `Session.handleAgentEvent` | `agentEventHold.wait` (no hold: returns), `recordNestedCalls` (handles message_start/end and agent_end only), `extensionEventHook` → `emitMessageUpdate` (no handler: nothing), `notifyOnEventExecutor` (no listener), `emitPublishedEvent` → `sendEvent` (discarding: `AcknowledgeEvent` is a no-op for a non-barrier event). A no-op event enqueued behind others only delayed the executor, which then did nothing for it. | `TestUnobservedMessageUpdatesAreNotDelivered` (agent): every other event and the transcript are identical to a run whose listener sees everything. |
| Awaits: `emitObserved` `Yield`, the Session `Yield`, `AwaitContinuation` (reserve + release) | Points where another ready continuation could run before the consumer went on. | See below. |

### The skipped awaits cannot change an observable interleaving

The only continuation that can be ready while the consumer handles an update is the provider's. It never waits for the consumer: `push` appends to an unbounded queue (or hands the event to a waiting `next`) and publishes the partial under the stream lock, and nothing in the producer reads consumer state. So the provider performs the same sequence of actions in the same order whether or not the consumer awaits: SSE reads, `provider_stream_event` and request hooks, publications and pushes. Only the position of the consumer's skipped steps among them changes, and those steps do nothing observable.

The Session executor goroutine runs only events queued to it, and the skipped update queues nothing. An external completion (timer, I/O, an extension's reply, a `HoldAgentEvents` release) runs only after every ready reaction has run (`AwaitExternal`; Node runs microtasks before the next macrotask). At that point, in both runs, the consumer has consumed every queued event and waits on an empty queue, and the provider waits at the same I/O. The Agent's state is that of the last update consumed. Everything an external callback can read or do, including registering a listener or an extension handler, therefore sees the same state as on the base, and the next update after an observer appears is delivered exactly as on the base.

`TestMessageUpdateObserverAppearingMidStreamSeesEveryLaterUpdate` (agent) checks this with paced chunks, so each chunk is pushed from its own timer callback. The observer starts after 1, 2, 5, half and all-but-one updates, and receives exactly the updates, message JSON and event JSON, that an always-observing listener receives from that point on.

An Agent or Session listener added from another goroutine during a stream races the stream on the base too. The predicate is asked again for every update, so such a listener receives every update after the one it raced with. The termination hold is checked for every update, so the Agent stops at its next event, as before.

## Observer kinds (each observed path is the base path)

| Observer | Predicate true because | Proof (by name) |
|---|---|---|
| JSON mode | Session listener (print-mode subscriber) and Agent backpressure listener (`Subscribe`) | `TestPrintTextModeDeliversMessageUpdatesToAnExtensionHandler` compares JSON-mode handler payloads with text mode's. The existing coding/cli json/print oracles in the named run are green. |
| RPC | Session listeners (`rpcHost.forward`) and Agent `Subscribe` | `TestRPC33ObservationMatrix`, `TestRPC33ObservationMatrixAzure`, `TestRPC33ObservationComparator` |
| Extension `message_update` handler | `HasExtensionHandlers("message_update")` | `TestPrintTextModeDeliversMessageUpdatesToAnExtensionHandler`: in `-p` text mode the handler receives every update, each identical to JSON mode's. `TestSessionExtensionUpdateUsesCurrentMessage` |
| Agent subscriber | `Subscribe` / nil predicate | `TestAgentClassMatchesPi`, `TestAgentE2E_EmitsLifecycleUpdatesWhileStreaming` and the agent message_update tests in the named run |
| TUI | Events consumer (`DiscardEvents` not called) | `TestSessionListenerSeesEveryAssistantLifecycleEventAtPiReactions`, `TestSessionInlineDeliveryMatchesExecutorDelivery` |

## Red and green

`TestPrintTextModeUnobservedDeltasAllocateLittle/faux` (coding/cli) measures allocations per streamed 16-byte argument delta in `-p` text mode as the slope between 1000 and 5000 deltas. On team/tern/lw-quiet-red (base + test) it reads 45.0; the ceiling is 20; with this change it reads 13.5.
