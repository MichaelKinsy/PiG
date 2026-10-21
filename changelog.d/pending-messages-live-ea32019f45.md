### Fixed

- An extension's `ctx.hasPendingMessages()` now asks the Host instead of reading a cached state snapshot, so it reports a steering or follow-up message queued while a handler is awaited, as Pi's live getter does. Extensions that gate delivery on that flag, such as `pi-queue-steer`, no longer report `Pi did not accept the queued message at this delivery boundary` after a message that was delivered. <!-- fix: ea32019f45 -->
