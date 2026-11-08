### Added

- Chord: `Host.Delegate` lets a host with an address take the remote types it does not serve itself over a route to another host (the Go app kit's server serves its Node plugins' objects through it), so the server serves the union of its own types and the plugins'; a host without one is unchanged.
