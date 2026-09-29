### Fixed

- OpenAI Completions and Responses now return their Event Stream before HTTP setup settles and admit start without waiting for the first body chunk. Cancellation and setup failures remain terminal stream outcomes.
- Stream partials expose independently owned observations while retaining full or shallow message-reference behavior. Final OpenAI tool calls remove their temporary parser fields.
- Message observations preserve null tool arguments and numeric argument types, so historical session serialization does not panic and tool validation does not change retained arguments.
- JSON/RPC message updates no longer serialize or traverse discarded cumulative partials when projecting usage and event fields.
- JSON mode converts and serializes events during the awaited Session notification, before persistence, while continuing to drain output. Serialization failures now fail the run instead of being silently dropped.
- Agent stream creation and iterator adoption share one continuation, so provider progress cannot overtake the caller's synchronous prefix. Providers and subscribers retain the same active abort signal.
- OpenAI streams retain SDK-buffered values during cancellation and preserve the provider's finalization and abort-message boundaries.
