### Fixed

- The in-memory telemetry recorder stored two copies of each event's time, so the second and later events of a span read another event's time in stored traces. Each event keeps its own.
