### Fixed

- Release archives of `pig-acp` now bundle `@pi-in-go/pigpen-acp` 0.2.4, which reads PiG's own version from `pig version` (with a bounded `--version` fallback) instead of displaying the upstream Pi version from `pig --version` in Zed thread titles and update notices. <!-- fix: 7e3d3c737b --> <!-- cause: automation/release/pig-acp.lock pinned 0.2.3 whose pigVersionHeader ran pig --version -->
