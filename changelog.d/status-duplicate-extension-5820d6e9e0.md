### Fixed

- `pig status` no longer reports a shared extension name as an unhealthy duplicate. Two Packages that both expose `src/index.ts` resolve to the same `"src"` public name, and the loader identifies an extension by its path: it renames the later copies (`"src:2"`) and loads them all, as Pi loads every extension path. The report keeps them enabled and moves the finding to a new `warnings` list, so `pig status` exits 0 and `pig status --json` stays `"healthy": true`. Skills, prompts and themes key their runtime set by name, so their duplicates remain errors. <!-- fix: 5820d6e9e0 -->
