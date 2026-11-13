### Changed

- Chord hosts that run Pi's TypeScript (a plugin's Node subprocess, a page, the terminal client) follow Pi main 47e9d74f9 and 90ed3e3b1: the remote references of one host with the same type, version, params, placed address and caller share one hold over a link. A second reference joins it with no message, the last release sends `release`, and releasing one of several cancels only its own calls. A hold's `chord.hold` span is now a root span of the host's telemetry, linked to the span of its first getter, so an exporter sees it outside the getter's trace.
