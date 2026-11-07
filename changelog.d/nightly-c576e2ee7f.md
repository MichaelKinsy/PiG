### Changed

- CI: the nightly workflow's race and allocation jobs prepare the pinned Pi oracle with `make upstream-mirror`, the step `ci.yml`'s Linux job runs, so the Pi-oracle tests find the npm trees they import from (`durable/interop`, `internal/typebox/testdata/npm`, `ai/testdata/sdk`) and the two workflows cannot list different installs; the race job installs the pinned SBCL as `ci.yml` does, which `cmd/pig`'s Lisp SDK test needs. The ai, libs, rest and cli shards failed every night with `ERR_MODULE_NOT_FOUND` and `sbcl is required`.
- CI: `FuzzMarkdownRenderStaysWithinWidth` allows the one row Pi's Markdown emits wider than the width, a double-width character at width 1 (`breakLongWord` appends a grapheme to a fresh row whatever its width); every other wider row still fails.
- The allocation benchmarks that start a program (`BenchmarkClipboardImageCommand`, `BenchmarkNpmMetadataLookup`, `BenchmarkPackageInheritedOutput`) run with `PATH` alone, so their allocs/op no longer follow the variables and `PATH` of the machine that runs them.

### Fixed

- Searching a long session in `/tree` allocates 30% less than in 0.7.0: lowercasing text with non-ASCII characters (the search text of every row, model ids and names, and other callers of `jsstring.ToLower`) made a second copy of each text, which raised `BenchmarkTreeRecompute` by 43%. <!-- fix: 1cfaf26848 -->
- `/fork` of a session that is not file-backed builds the clone's entries from the source's decoded messages again instead of decoding every record a second time, which doubled the allocations of that clone. <!-- fix: d1ee8838e6 -->
- Every frame the renderer paints allocates two fewer objects: the cursor's hide and show copied their escape sequence into a new slice, where they had copied it into a pooled buffer before dead-terminal handling routed them through `WriteToTerminal`. <!-- fix: 62f0353e7c -->
