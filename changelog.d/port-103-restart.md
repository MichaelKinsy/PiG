### Added

- A running session warns once, in place of the `/bug` hint, when an update or another `pig` replaced or removed the `pig` executable, or another `pig` pruned an extension cell or runtime file the session uses: `The pig installation this session runs from was removed or replaced. Features that load code on demand can fail until restart.`, with the `pig --session <id>` command that continues the session. PiG checks only when an error is shown (an assistant error, a failed tool call, an error or extension error notice). This is PiG's version of Pi 1.0.3's install-change warning.
