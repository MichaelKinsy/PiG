### Added

- chord: `BuildBase` takes a `BaseResolution` (`Conditions`, `Tsconfig`), and `WebSurfaceOptions`, `TuiSurfaceOptions`, `AppWeb` and `AppTui` take `Tsconfig`: a base bundle resolves packages through that tsconfig's `paths`, so a source checkout without `dist/` builds its bases; without it no tsconfig.json is read (Pi 53af02378). <!-- feat: f32c342b54 -->
