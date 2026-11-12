### Changed

- Anthropic message threads are now on by default for any provider whose base URL is on `api.anthropic.com`, not only the `anthropic` provider, as in Pi. Other endpoints still need `compat.supportsMessageThreads`. <!-- fix: 704bed4d93 -->
