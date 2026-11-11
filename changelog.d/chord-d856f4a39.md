### Fixed

- `host.Registry` with a short `ReadyTimeout` no longer fails "did not become live" when the machine delays the waiting goroutine past the deadline: in Pi the wait's publication, lookup and first usable check run before any timer, so the deadline reports why the registry cannot be read (`contract_mismatch` for a version the provider cannot serve, `unavailable/undeclared` for a registry nobody serves). A deadline that finds the host published and the first check not yet made now lets that check run first. <!-- fix: d856f4a39 -->
