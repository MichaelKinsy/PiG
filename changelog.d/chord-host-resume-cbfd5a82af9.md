### Added

- Go API `chord/next/host`: `host.Resume(ctx, work)` runs `work` as one synchronous section of the host's loop on the goroutine of a method handler that suspended (the awaits of Pi's handler resume inside the loop, so code that reads the host's state after a transaction or a blocking call reads it there); a ctx that is no handler's is refused.
