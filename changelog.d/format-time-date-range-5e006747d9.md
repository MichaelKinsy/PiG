### Fixed

- `FormatTime` returns an options error for an instant outside JavaScript's Date range (more than 8.64e15 ms from the epoch), where Pi's `Intl.DateTimeFormat.format` throws a RangeError; it printed a clock time for it before. The ends of the range are still formatted. <!-- fix: 5e006747d9 --> <!-- cause: D465 recorded the out-of-range instant as formatted because Go has no invalid Date, but no range check on the time value was ever added. -->
