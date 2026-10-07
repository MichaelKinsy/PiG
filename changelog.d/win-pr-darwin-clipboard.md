### Fixed

- On macOS, reading an image or file paths from the clipboard and writing text to it no longer risk passing AppKit an address in freed memory. When Go moved the goroutine stack during the call, AppKit could read a stale array or text buffer. These buffers now stay in place for the call.
