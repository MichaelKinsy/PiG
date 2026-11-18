### Changed

- Raised `github.com/alecthomas/chroma/v2` from 2.24.1 to 2.27.0 (and added its `regexp2/v2` dependency). Only the portlens parity-evidence tool in `automation/` links chroma; `pig` and its syntax highlighting (the highlight.js port) do not, so the shipped binary and highlight output are unchanged.
