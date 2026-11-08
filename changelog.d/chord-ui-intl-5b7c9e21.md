### Changed

- Chord: `FormatNumber` prints what the pinned Node's `Intl.NumberFormat` prints (symbols, grouping with minimum grouping digits, numbering systems, half-away-from-zero rounding; the review's 312 differing outputs are 0) and `FormatTime` is ported (`TimeOptions`: `timeStyle` short or medium, or hour/minute/second, with `hour12`). Both read tables generated from the pinned Node's ICU (`make intl-tables`, part of `pin-bump`) and return `(string, error)` for what `Intl` throws. A drift test diffs the tables against a live Node.
