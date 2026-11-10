### Changed

- Listing the available models builds the catalog of a Radius provider only when the provider has auth: with no Radius credential it no longer clones and rebuilds every Radius model to discard them, which cost about 550 of the 910 allocations of one call with a Copilot account (364 after, measured at `-benchtime=5x`; the catalog grew with the Pi pin follow (f8f335a21) and took the call to 1002-1056). The `BenchmarkCopilotAccountAvailability` baseline in `automation/ci/allocbase` is lowered to 364.
