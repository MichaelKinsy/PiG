### Changed

- Share one `pi.events` bus across every Node extension process, including explicitly isolated extensions and exact standalones. A listener in another process receives the emitter's original payload through cross-process references, so synchronous mutations, reentrant emits, retained and post-await aliases, and callbacks behave as in Pi. D77 is retired; D83 names the remaining cross-process boundaries.

### Fixed

- `OverlayHandle.unfocus({ target })` in a Node extension focuses exactly its target when the target is `null`, the extension's installed editor component, or another of its mounted overlays, as in Pi. It previously threw for every target (D73).
