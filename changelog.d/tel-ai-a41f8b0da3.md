### Upgrading

- `ai` renames the provider request option `TelemetryContext` to `Telemetry` (Pi `telemetryContext` → `telemetry`, Pi 2ec55d80) on `StreamOptions`, `ClassifierOptions` and `ImagesOptions`, with no alias. Its type is `telemetry.TelemetrySpan`: pass a parent span, not an adapter; nil is the omitted option and uses the no-op span.
- `ai.LazyStream` and `ai.LazyStreamSync` take an optional trailing `LazyStreamObserver` (Pi's `lazyStream(model, setup, observer?)`).

### Added

- `ai.AITelemetrySchema()` (Pi `AI_TELEMETRY_SCHEMA`, built on first call) and opt-in `pi.ai.request` / `pi.ai.poll` spans around `Models.Stream`, `StreamSimple` and `StreamDeferred` (and their completion helpers): `pi.ai.first_chunk` and one `pi.ai.block` event per completed text, thinking or tool-call block (never per delta, never content), final `stop`, `usage.*`, `cost.total` and `response.id` attributes, and duration, first-chunk, token and USD cost metrics through the parent span's `Metrics()`. A failed request reports status `provider_error` with at most 500 error-message characters; a cancelled one keeps status `ok` with `stop: "aborted"`. Without a parent span nothing is observed and the stream path allocates as before.
