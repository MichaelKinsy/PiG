### Fixed

- `pig mcp login -l` and `pig mcp logout -l` no longer fail with `Unknown option --local.`, as `pig mcp add` and `pig mcp remove` already accept `-l`; with `-l` they require the server to be a project server. Unknown options are reported as typed, and a mistyped server name gets a suggestion, for example `Did you mean "fixture"?`. Carried forward from Pi be15fe3c (a Pi commit without an issue number), past the 1.4.0+b3a29f11 pin. <!-- fix: be15fe3c -->
