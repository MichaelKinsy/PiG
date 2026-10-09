### Fixed

- Reduced context-limit request failures by estimating input at 3.5 characters per token instead of 4 when calculating output limits.

### Added

- Added `+name` and `-name` entries to `--tools`, which change the default tool selection instead of replacing it, for example `pig -t +codemode`. A reload keeps tools removed with `-name` removed.
