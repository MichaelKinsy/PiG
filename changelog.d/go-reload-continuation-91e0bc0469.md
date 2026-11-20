### Added

- Go extensions can pass `sdk.ReloadOptions{ContinueWith: ...}` to `Context.Reload` to submit one follow-up user message through the replacement runtime after reload, scheduling the reload after the current turn settles when called from a busy interactive tool.
