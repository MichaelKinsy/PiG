### Fixed

- Chord's objects no longer answer a call `not_ready` during a reload while the caller sees them `live`: a call over a link to an object whose restart had drained, sent while its hold was still bound to the old instance, got `not_ready: ... not live yet`. Such a call now waits for the object's new attempt (live, failed or ended), bounded by `readyTimeout` (`unavailable/park_timeout`); a parked attempt showing `failed { waiting }` rejects it with that cause. <!-- fix: f5efecf629 -->
