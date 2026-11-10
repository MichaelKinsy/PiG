### Changed

- Listing the available models builds the catalog of a Radius provider only when the provider has auth: with no Radius credential it no longer clones and rebuilds every Radius model to discard them, which cost about 550 of the 910 allocations of one call with a Copilot account (364 after, measured at `-benchtime=5x`; the catalog grew with the Pi pin follow (f8f335a21) and took the call to 1002-1056). The `BenchmarkCopilotAccountAvailability` baseline in `automation/ci/allocbase` is lowered to 364.
- Reading the full model catalog and rebuilding the request-auth runtime no longer build the bundled Radius catalog only to replace it, and the Radius baseline is no longer copied a second time at construction: `BenchmarkRuntimeModelsSelection` 811 to 604 and `BenchmarkRequestAuthProviderProjection` 815 to 599 allocations per call (`-benchtime=200x`; the gate baselines are lowered to the measured 595 and 598).

### Fixed

- A runtime API key (`--api-key`, `SetRuntimeAPIKey`) for a Radius provider now counts as its auth, as it does in Pi: the provider has configured auth, its models are in the available list and it counts as an available provider. Before, requests used the key but the Radius catalog was not listed. <!-- fix: a17e2a163e4 -->
