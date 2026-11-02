### Fixed

- `PI_TUI_ESC_TIMEOUT` now waits as Node's `setTimeout` does for the lone-Escape timer: a fractional value is truncated, and a value under one millisecond or over 2147483647 waits one millisecond instead of that many nanoseconds or about a month. <!-- fix: fe4bd27863 -->
