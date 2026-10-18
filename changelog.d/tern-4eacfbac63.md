### Fixed

- A Durable core session with many live tasks recomputes its next wake without walking every live task on each commit: ending a chain of 20,000 held tasks takes under a second instead of about a minute.
