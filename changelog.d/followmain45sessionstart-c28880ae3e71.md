### Fixed

- Follows Pi 6aab551a79ba: `--mode json` dropped the events emitted by extension `session_start` handlers, such as `entry_appended` for entries they append, `session_info_changed` for a name they set and the `message_start` and `message_end` of a custom message they send, at startup and after an extension replaces the session (`ctx.newSession()`). The JSON writer now subscribes before the extensions are bound, so these events follow the session header and come before the prompt's events. The fault is in v0.7.0. <!-- fix: c28880ae3e71 -->
