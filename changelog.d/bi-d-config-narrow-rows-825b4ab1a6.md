### Fixed

- `pig config` no longer clips `  No resources found` and the `(n/total)` scroll row to the terminal width, as Pi does not; the selector's rows still fit every width at which Pi's fit. Declared Package members whose file is absent stay listed with a `  missing` suffix, recorded as D230. <!-- fix: 825b4ab1a6 -->
