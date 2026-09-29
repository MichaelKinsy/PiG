### Fixed

- Fixed the assistant message a consumer observes at each `mistral-conversations` delivery differing from Pi's, including the `message_start` an RPC client reads. The provider loop now spends the same number of microtasks per await, yield and rejection as Pi's `readMistralEvents`, its `for await` and its async return, so every event describes the message at the same point of the stream. A tool call also exposes its `partialArgs` scratch buffer while it streams, as in Pi.
- Fixed Mistral streams reporting Go's `context canceled` instead of Pi's `This operation was aborted` when the request is aborted, and Go's JSON error text instead of V8's `JSON.parse` message for a malformed record.
- Fixed Mistral streams skipping empty text deltas: an empty `content` string now opens a text block and pushes a `text_delta`, as in Pi.
- Fixed a Mistral `finish_reason` of `error` or an unknown reason ending the stream early. Pi records the reason and keeps consuming the body before it reports the error, and it reports the error without closing open blocks.
- Fixed Mistral usage, the stop reason and the raw stop reason being assigned only when the stream ended. They are set as each chunk arrives, and the usage object is updated in place.
