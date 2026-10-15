### Upgrading

- Go API `chord/host`: `Host.Get` and `Ctx.Get` wait until the reference is ready: live, with every state member of its variant. Use `Host.Hold` for the synchronous reference that may still be loading. Before: `ref, _ := host.Get(typ, input, nil)` then `ref.Ready(ctx)`. After: `ref, err := host.Get(typ, input, nil)` for an awaited reference, or `ref, _ := host.Hold(typ, input, nil)` for the old behavior. A failed `Get` releases only its own reference and returns the activation error, `aborted` (`GetOptions.Signal`, or the ctx closing), or `unavailable/ready_timeout` after `HostOptions.ReadyTimeout` (60 s).
- Go API `chord/host`: reading a state member of a reference that is not live yet panics with a `not_ready` ChordError (and `not_in_variant` for a member the variant lacks) where it returned nil. A listener added before the first value gets its snapshot then: the events are `snap`, `ops`, not `snap` with nil, `reset`.
- The Chord link protocol version is 4. A hold implies the state of its variant (`state:false` opts out), state frames are addressed by hold and member (`as: snap | reset | ops | error`, `value`), `sub`, `unsub` and `resync` work per hold, a hold from a client link carries an `origin` the server assigns, and shape frames carry no `codeVersion`. A peer of another version closes the link with `protocol-version`.

### Added

- `chord/host`: one shared binding per host and key for remote references. References to one object share one hold, one snapshot per member and one frame per change. `HostOptions.Linger` keeps a released binding for a time (cap 4), `GetOptions.State` false gives a method-only reference, and a re-hold after a link loss keeps the last values readable.
- `chord/host`: a local object goes live only with its state, also where it forwards or derives from a remote reference's member; activation and a reload's prepare wait at most `HostOptions.ReadyTimeout` and name what they wait for.
- `chord/host`: `LinkOptions.PendingCap` bounds the state ops that wait for a slow peer on one link (default 4 windows). Beyond it the oldest streams are replaced by their current value, and a relay asks the provider for a reset.
