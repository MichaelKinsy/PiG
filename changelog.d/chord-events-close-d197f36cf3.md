### Added

- Chord: plugins on a Node host can declare event members (`event(schema)` in a local contract, `events` in a remote variant), emit with `self.events()` and subscribe with a reference's `on`: the vendored Node tree of the plugins component carries Pi's chord events (Pi 8d30e6306). The events are delivered inside the Node host; they do not cross to the Go host yet.

### Changed

- Chord: the two vendored chord trees (`chord/nextnode/plugins/node` and `chord/nextnode/surfaces/client`) are made the same way, from the pin plus the carried Pi commits their manifests list (`PI_CARRIED`), and `make vendor-chord-carried-check PI_REPO=<a Pi clone>` proves that each tree is its manifest's commit plus those commits.
