### Fixed

- chord: a list or live query whose value was set several times in one commit (a list's row moving from one loaded page to another) published one frame per set; over a link that was two entries for one member in one state message, the protocol error `member twice in one state`, which closed the link (Pi 36c709e720b6). It publishes one frame per commit now: its last value, diffed from the value before the commit. <!-- fix: 94a2516ddeb8 -->
