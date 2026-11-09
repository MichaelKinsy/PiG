### Added

- Chord app kit: `AppOptions.GoPackages` offers Go packages (`plugins.GoPackage`: Go functions and JSON values) to the server's TypeScript plugins. A directory plugin that lists the specifier in its `chord.external` imports it and calls the Go function; single-file plugins and web builds cannot. <!-- feat: 2746e64eac3c776eab108559ad552ead3194d574 -->
