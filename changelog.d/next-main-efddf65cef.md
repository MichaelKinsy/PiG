### Upgrading

- Go API `chord/host`: `CreateHost` needs a principal for the host's own code, as Pi 1.3.0's `createHost({ self })` does, and panics with a `*TypeError` without one. Before: `CreateHost(HostOptions{Name: "server"})`. After: `CreateHost(HostOptions{Name: "server", Self: "host"})`. `HostPrincipal` is gone: PiG no longer makes up principals.
- Go API `chord/host`: `Connect` takes `ConnectOptions` with the principal the parent's own requests act as, and `HostOptions.Upstream` is `&UpstreamOptions{Link: link, ConnectOptions: ConnectOptions{Principal: ...}}`. `Route.Principal` is required. Before: `host.Connect(link, LinkOptions{})`. After: `host.Connect(link, ConnectOptions{Principal: "server"})`.
- Go API `chord/host`: a remote contract says where its objects live and a get no longer names a host. Before: `Remote("t.chat", RemoteDef{...})` and `host.Get(chat, input, &GetOptions{At: &name})`. After: `Remote("t.chat", RemoteDef{..., Host: NewHostFamily("session:", "session")})` and `host.Get(chat, input, nil)`. `GetOptions.At` is gone.
- The Chord link protocol version is 3. A PiG host and a Pi host that speak another version close the link with `protocol-version` and report it through `OnProtocolMismatch`.

### Added

- `chord/host`: `Self.State` takes a `StateInit` as the initial value, so a cell's first value comes from its source. `Init` runs after the object's resources are open and before the object goes live, and a reload runs it for a cell it adds. Until then the cell's `Value` fails `not_ready`.
- `chord/host`: contract placement. `NewHostNamed` and `NewHostFamily` give a remote contract its host. `Serve` refuses a type the host is not the place of, a host refuses a hold whose input its own contract places elsewhere (`invalid/placement`), and a relay refuses a hold whose target disagrees with its contract token. The placement is part of the description and the fingerprint, and an `Accepts` adapter never bridges two placements.
