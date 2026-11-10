### Changed

- `ai.GetCacheWarmingRequestOptions` takes the request's context first, `(signal, options)`, like every Go call that is cancellable; it was `(options, signal)`.
- `tui.NewOAuthSelectorComponentAnimated` takes Pi's constructor order, `(mode, providers, onSelect, onCancel, initialSearch, ui)`, with the `tui.TUI` whose `RequestRender` the brand animation calls where it took a bare render function first.
