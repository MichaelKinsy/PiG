### Fixed

- `chord/nextnode/workers`: a worker that ended (was stopped or killed) while its Node plugin host was still starting no longer leaves a `chord-plugins-*` socket directory in the temp directory; the server removes what the worker's process left once it has ended. <!-- fix: eeee29b9a8 -->
