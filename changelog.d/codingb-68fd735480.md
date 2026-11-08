### Fixed

- grep takes its context lines from ripgrep instead of reading each matched file, reports the match limit only when another match exists, keeps a lone CR inside a line, and matches a glob relative to the searched directory. <!-- fix: 68fd735480 -->
