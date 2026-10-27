### Added

- `delta.PendingOps` holds the op batches a slow consumer has not taken yet and folds them while they wait (appends and primitive sets per path, `r` drops what came before, past 10,000 ops only "replace with the current value" stays). Ports Pi's `PendingOps`.

### Changed

- `chord.Watch` queues frames while its listener is busy or not started and delivers one call with the newest value, the folded ops (or a root replacement after an overflow) and the newest frame's context, instead of one call per frame with a 100-frame collapse. `chord.NewWatch` no longer takes a `replace` function; `chord.NativeWatch` still delivers every value in order and takes the optional `replace`. Durable's event stream keeps every event batch in order; document, view and task-graph watches fold.
