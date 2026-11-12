### Fixed

- In the Go extension SDK, a tool or command that the host cancelled right after sending its request could start with its context still open and its host calls sent: the cancel frame was read before the main loop armed the request and was applied only later. The SDK now keeps the cancel frames it reads for a request it has not armed yet and applies them when it arms the request, so the handler starts cancelled (or with its signal aborted for a signal-only cancel) and its host calls are refused. <!-- fix: 8defe805741 -->
