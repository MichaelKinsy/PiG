### Added

- Go extensions can use `Context.ReloadWithOptions` with `sdk.ReloadOptions{ContinueWith: ...}` to submit one follow-up user message through the replacement runtime after reload. A busy interactive tool schedules the reload for after the current turn settles. The existing `Context.Reload() error` is unchanged.
