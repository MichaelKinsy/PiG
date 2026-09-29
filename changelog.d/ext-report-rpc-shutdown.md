### Fixed

- Shut RPC mode down in Pi's order: publish already-queued Session events, detach the stdout subscription, emit `session_shutdown`, then disconnect and abort the run. The aborted run still delivers its `turn_end` boundary and `agent_settled` to extensions. A boundary without a persisted assistant entry reports Pi's `extension_error` on stdout.
- Restore signal termination during an RPC shutdown, and exit 0 at once when stdin ends during a signal-triggered shutdown. Stop owned extension processes without awaiting suspended handlers.
- Leave unanswered RPC dialogs and unsettled shutdown Promises pending when stdin ends. A Node event-loop drain ends the process without fabricating a handler result.
- Preserve the deterministic shutdown-notification-before-command-response order when an RPC extension command requests shutdown.
