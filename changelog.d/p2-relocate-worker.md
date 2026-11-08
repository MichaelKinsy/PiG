### Fixed

- p2: a session's relocation answers once the restarted worker's start settled (`SessionWorkers`' restart waits for the start its retry began, which the route reports, instead of a fixed 50 ms), and a relocation step that finished first no longer panics a wait on the workers' state ("Watch is stopped"). <!-- fix: p2-workspace -->
- p2: PowerShell reads the system of an environment's machine through any environment that reports it (`SystemReporter`, as Pi duck-types `connection.info()`), not only the two connection-backed ones; `OffersPowerShell` is exported as Pi's `offersPowerShell`. <!-- fix: p2-workspace -->
