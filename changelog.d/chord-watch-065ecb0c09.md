### Upgrading

- **Breaking (Go API):** `chord`, `durable/session`: the watch handle implementation moves from Durable to Chord, as in Pi. `session.CommittedWatch`, `session.NewCommittedWatch`, `session.NewCommittedWatchOn`, `session.DeliveryScheduler` and `session.NewDeliveryScheduler` are removed. Use `chord.Watch` with `chord.NewWatch(scheduler, value, detach, report, replace)`, and `chord.DeliveryScheduler` with `chord.NewDeliveryScheduler()`. Code inside Durable builds a Session watch with `session.NewSessionWatch(session, value, detach, replace)`.

### Added

- Go API `chord`: `Watch`, `NativeWatch` and `NativeWatchHandle`, the one watch handle implementation (Pi 42cb822f1). `NativeWatch` delivers in-process values with a listener `(ctx, value)` and no ops; an overflow collapses to the newest value.

### Changed

- Document, conversation view, task graph and event watches use Chord's `Watch`; behaviour is unchanged.
