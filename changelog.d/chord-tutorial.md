### Added

- The Chord tutorial in PiG's API is complete: `docs/chord/tutorial.md` has Pi's chapters (one server, a list on its own worker, routes, a page, a terminal, a theme, a second user, languages, changing code, moving a list, telemetry with a trace viewer, testing one plugin) with eight screenshots, and `examples/chord-todo` is the Go app and TypeScript plugins they quote. `tutorial_test.go` does Pi's three checks for every chapter, `screens_test.go` takes the screenshots in headless Chromium and checks what the book shows of them, and `lists_test.go` is the lists plugin's own test.

### Fixed

- The app kit gives the HTTP component the app's telemetry store (as Pi's `startServer` does), so each request is recorded as a `chord.http` trace of the server. <!-- fix: d06b3d225b9 -->
