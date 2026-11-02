### Fixed

- An extension dialog (`select`, `input`, `editor`) whose `timeout` carries a fraction of a millisecond past a whole second, such as `1000.5`, now starts its countdown from the next second like Pi's `Math.ceil(timeoutMs / 1000)`; the countdown rounded the milliseconds down to whole milliseconds first and started one second short. <!-- fix: 776d60b196 -->
- A model request from a subprocess extension whose integer field is `2^63` or larger is now rejected instead of converting to the largest `int64` on arm64. <!-- fix: 776d60b196 -->
