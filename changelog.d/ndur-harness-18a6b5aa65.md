### Added

- `harness.GenerationHooks.BeforeProviderRequest(ctx, ProviderRequest{Model, Payload}, api)` replaces the provider request body of a generation request, for that request only, for example to set a provider-specific field. The body is in the provider API's format; the hook returns `&ProviderPayload{Payload}` or nil. Hooks chain in extension order and run each time the provider builds a body; a returned error is reported and the chain continues. Compaction requests do not use it (Pi `d280e1ff`).
