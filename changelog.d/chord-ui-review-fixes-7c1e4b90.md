### Fixed

- Chord's surfaces: a language or theme follower stopped before its pick watch bound no longer crashes the process ("Watch is stopped"); a follower's callback may call `SetDevice`, `SetMode` or `Stop` on it without deadlocking; `Message(key)` with no arguments sends `params: []`; `CanonicalLocale` and the text direction of a locale follow Node's `Intl` (`en_US`, `i-klingon` and `zh-min-nan` are refused, `cmn` is `zh`, `mo` is `ro`, `sam` is right to left).
- Chord's `TuiScope`: a holder that releases its own `Hold`en reference before the scope's watch of its state started no longer crashes the process.
- Telemetry: a rendered trace keeps a lone surrogate beside an escaped control character.

### Added

- Chord's app kit: device pairing (`/_chord/pair`) and data archives (`PackData`, `PackStore`, `UnpackData`) in `chord/nextnode/app`, and `HttpRequest.RemoteAddress` in `chord/nextnode/http`.
