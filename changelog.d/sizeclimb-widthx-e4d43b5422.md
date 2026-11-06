### Added

- The Go extension SDK exports its terminal-width engine as the package `github.com/MichaelKinsy/PiG/extensions/sdk/tui/widthx`: `VisibleWidth`, `TruncateToWidth`, `WrapTextWithAnsi`, `SliceByColumn`, the grapheme and ANSI helpers and the rest of the Pi-faithful width functions, with the same behavior as before. The `extensions/sdk/tui` helpers are unchanged.

### Changed

- The `pig` binary carries one copy of the terminal-width engine and of its Unicode and emoji tables instead of two, and the emoji table no longer pays a header for each of its 2760 sequences: the stripped binary is 115 to 132 KB smaller on Linux, macOS and Windows. Terminal widths, wrapping and truncation are the same.
