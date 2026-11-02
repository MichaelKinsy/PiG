### Fixed

- A Go extension that is sent an `events.dispatch` request while it loads, before the host's `ready`, no longer answers it under another frame's ID with another frame's payload. The listener's goroutine shared the receive loop's envelope variable, which the next received frame (a second dispatch, or `ready`) overwrote while the listener ran; `go test -race` flagged the read and the write. Each listener now keeps the request that started it. <!-- fix: 7475a64e35 -->
