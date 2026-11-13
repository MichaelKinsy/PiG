### Fixed

- Interactive mode: when the first message flushed from the compaction queue was rejected (a `before_agent_start` handler that fails, a missing model, an input handler error), a message queued behind it could start a run of its own if the rejection had already ended the first run, and be rejected for the same cause, so "Failed to send queued messages" was shown twice. The rejection is now recorded when the run finds it, and a message behind it is neither sent nor queued; the whole queue comes back with one failure shown. <!-- fix: f81ce001e32 -->
