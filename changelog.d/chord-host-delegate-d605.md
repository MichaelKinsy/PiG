### Added

- Chord: `Host.Delegate` lets a host with an address take the remote types it does not serve itself over a route to another host (the Go app kit's server serves its Node plugins' objects through it), and `Host.Serves` tells a delegate's host what that host serves, so a server plugin's object can hold the app's own remote types (the locale and theme choices, telemetry) as Pi's one server host lets it: the server serves the union of its own types and the plugins' both ways; a host without a delegate is unchanged.
