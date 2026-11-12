### Fixed

- Chord surfaces: a client holds the scripts object by the time `StartClient` returns, as in Pi. The hold was made in the background, so it could be missing right after the start. <!-- fix: 862a8f7a90 -->
