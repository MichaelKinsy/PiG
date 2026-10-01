# Bedrock modeled-exception frame: what real Pi forwards to onProviderStreamEvent

Question: `bedrock-raw-stop-reason.test.ts:91` ("forwards SDK error items before reporting them") expects the hook to receive `[messageStart, {internalServerException}]`. Does real Pi ever do that?

Method: `bedrock-exception-frame-probe.mjs` runs the pinned Pi package's real `streamBedrock` (`dist/api/bedrock-converse-stream.js` of the installed pi-ai package; its `src/api/bedrock-converse-stream.ts` is byte-identical to the pinned mirror's copy) against a local HTTP/1 server (`AWS_BEDROCK_FORCE_HTTP1=1`) that sends a real AWS event-stream `messageStart` event frame followed by an `exception` frame (`:exception-type` internalServerException, body `{"message":"bedrock stream failed"}`). Run with `PI_AI_DIST=<pi-ai dist>`, Node 24, a temporary `HOME`.

Result (real Pi): `{"received":[["messageStart"]],"stop":"error","err":"Internal server error: bedrock stream failed"}`. A plain `messageStart` + `messageStop` run received both items, so the hook and the probe work.

Reason: `@smithy/core` `getMessageUnmarshaller` (`event-streams/eventstream-serde-universal/getUnmarshalledStream.js`) throws the deserialized exception for `:message-type` exception frames before Pi's `for await` body runs, so an exception frame never reaches `item.internalServerException` (bedrock-converse-stream.ts:318-327).

PiG on the same frames (`TestBedrockProviderStreamEventsUpstream/forwards_SDK_stream_items_before_an_exception_frame_ends_the_request`): hook receives `[messageStart]`, result `error` with `Internal server error: bedrock stream failed`. Identical.

## Correction: the item branch is reachable through event frames

The exception-frame result above does not make `:318-327` dead code. `getMessageUnmarshaller` deserializes an `event` frame through the same union deserializer, and the ConverseStream union models the five exception members. `bedrock-exception-event-probe.mjs` sends `messageStart` followed by an `event` frame whose `:event-type` is each member. Real Pi 0.99.2 forwards both items and throws the member:

```
internalServerException     [messageStart, {internalServerException}]     error "Internal server error: bedrock stream failed"
modelStreamErrorException   [messageStart, {modelStreamErrorException}]   error "Model stream error: bedrock stream failed"
validationException         [messageStart, {validationException}]         error "Validation error: bedrock stream failed"
throttlingException         [messageStart, {throttlingException}]         error "Throttling error: bedrock stream failed"
serviceUnavailableException [messageStart, {serviceUnavailableException}] error "Service unavailable: bedrock stream failed"
somethingUnknown            [messageStart]                                error "Bedrock stream ended without a stop reason"
```

PiG dropped all five members and reported `Bedrock stream ended without a stop reason`. `bedrock-raw-stop-reason.test.ts:91` is therefore ported, not designed out: `TestBedrockProviderStreamEventsUpstream/forwards_SDK_error_items_before_reporting_them/<kind>` asserts the rows above, and the observer receives the Go SDK's `*types.UnknownUnionMember` for the member.
