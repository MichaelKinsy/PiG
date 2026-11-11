### Added

- p2's TUI `/settings` list filters its rows as you type, as Pi's settings do. <!-- feat: p2-tui-settings-search -->
- p2's TUI `/dev-restart` asks the server to restart under the dev supervisor, without a consent screen (the server could do it by itself); a request dropped as the server exits counts as requested. The Go server has no dev supervisor (D473), so it answers that dev mode is not enabled. <!-- feat: p2-tui-dev-restart -->
