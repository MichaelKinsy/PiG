### Fixed

- Durable with telemetry off (no telemetry or the no-op span) no longer builds span attributes, events or metric attribute bags, and `ai.Models.Stream` without a parent span captures nothing for the telemetry observer, so both allocate as before the telemetry integration; Durable's reflective restore also caches each struct type's field list. The AI telemetry schema is built on first use instead of at process start, and non-numeric attribute names no longer allocate in `jsstring.ArrayIndex`, which cuts the allocations of a recorded `ai` stream. <!-- fix: 692bdf76eb -->
