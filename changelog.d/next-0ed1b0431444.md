### Changed

- The upstream Pi pin moves from 1.3.0+1402973a to 1.3.0+0ed1b043. The model catalog follows it, including `MiniMax-M3.1-Flash` on `minimax` and `minimax-cn`, and the vendored Node extension runtime carries Pi's `@earendil-works/pi-telemetry` modules that `pi-ai` now imports. Pi's Durable, AI and Chord telemetry commits (2ec55d80, d9025a5f) are deferred to a later milestone: PiG still emits no telemetry spans.
