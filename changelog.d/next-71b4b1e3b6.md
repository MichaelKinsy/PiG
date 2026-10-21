### Changed

- Durable document definitions require a checkpoint policy, as Pi 5c1736f8 requires `checkpointWhen` on `defineDoc()` and `defineDocFamily()`: `durable.DefineDoc` and `DefineDocFamily` panic when a definition has neither `CheckpointWhen` nor `CheckpointWhenRaw`. Use a function that returns false to keep every change a delta. The nested-call, code mode run and code mode journal documents never checkpoint; the code mode store document writes a base after 64 deltas. The Cloudflare host facade requires `checkpointWhen` too.
