### Fixed

- A Session that an extension creates with `createAgentSession`, or that `coding.CreateAgentSession` returns, now reports `entry_appended` to its listeners when an extension that its resource loader loaded calls `pi.appendEntry`, as Pi does. The event arrives before the event the extension was handling, and `ctx.sessionManager.appendCustomEntry` still writes the log without an event. The Session wrote the entry and told no listener. <!-- fix: 1c6b8643abd7fa82af8c817229656696350eb6a9 -->
