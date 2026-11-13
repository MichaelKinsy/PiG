### Changed

- docs: the Chord tutorial (`docs/chord/tutorial.md`) shows the traces `examples/chord-todo` prints now that the references of one host share one wire hold: in the trap "a trace starts where a span starts" the server's hold of the list is shared work, a `chord.hold` root of its own linked to the request, so the trace of the request (step 11, and the refused request) no longer lists it or the worker's relay of it, and the listed `chord.http` traces count 6 spans (were 8).
